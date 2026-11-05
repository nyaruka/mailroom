package msgio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/queues"
	"github.com/nyaruka/gocommon/svclogs"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

type MsgOrigin string

const (
	MsgOriginFlow      MsgOrigin = "flow"
	MsgOriginBroadcast MsgOrigin = "broadcast"
	MsgOriginTicket    MsgOrigin = "ticket"
	MsgOriginChat      MsgOrigin = "chat"
)

type Contact struct {
	ID         models.ContactID `json:"id"`
	UUID       core.ContactUUID `json:"uuid"`
	Email      string           `json:"email,omitempty"`
	LastSeenOn *time.Time       `json:"last_seen_on,omitempty"`
	OtherURNs  []urns.URN       `json:"other_urns,omitempty"` // currently needed for WA handlers to know if contact already has a BSUID URN, may not be needed long term
}

type FlowRef struct {
	UUID assets.FlowUUID `json:"uuid"`
	Name string          `json:"name"`
}

type Templating struct {
	*core.MsgTemplating
	Namespace  string `json:"namespace"`
	ExternalID string `json:"external_id"`
	Language   string `json:"language"`
}

type Session struct {
	UUID       core.SessionUUID `json:"uuid"`
	Status     string           `json:"status"`
	SprintUUID flows.SprintUUID `json:"sprint_uuid"`
	Timeout    int              `json:"timeout,omitempty"`
}

var sessionStatusMap = map[flows.SessionStatus]string{flows.SessionStatusWaiting: "W", flows.SessionStatusCompleted: "C"}

// Msg is the format of a message queued to courier
type Msg struct {
	UUID                 events.EventUUID   `json:"uuid"`
	OrgID                models.OrgID       `json:"org_id"`
	Contact              *Contact           `json:"contact"`
	Origin               MsgOrigin          `json:"origin"`
	Text                 string             `json:"text"`
	Attachments          []utils.Attachment `json:"attachments,omitempty"`
	QuickReplies         []core.QuickReply  `json:"quick_replies,omitempty"`
	Locale               i18n.Locale        `json:"locale,omitempty"`
	Templating           *Templating        `json:"templating,omitempty"`
	HighPriority         bool               `json:"high_priority"`
	MsgCount             int                `json:"tps_cost"`
	CreatedOn            time.Time          `json:"created_on"`
	ChannelUUID          assets.ChannelUUID `json:"channel_uuid"`
	URN                  urns.URN           `json:"urn"`
	URNAuth              string             `json:"urn_auth,omitempty"`
	Flow                 *FlowRef           `json:"flow,omitempty"`
	UserID               models.UserID      `json:"user_id,omitempty"`
	ResponseToExternalID string             `json:"response_to_external_id,omitempty"`
	IsResend             bool               `json:"is_resend,omitempty"`
	PrevAttempts         int                `json:"prev_attempts,omitempty"`
	Session              *Session           `json:"session,omitempty"`
}

// otherURNs returns the identities of all URNs on the contact except the one being sent to
func otherURNs(mo *models.MsgOut) []urns.URN {
	var others []urns.URN
	for _, u := range mo.Contact.URNs() {
		if u.ID != mo.URN.ID {
			others = append(others, u.Identity)
		}
	}
	return others
}

// NewCourierMsg creates a courier message in the format it's expecting to be queued
func NewCourierMsg(oa *models.OrgAssets, mo *models.MsgOut, ch *models.Channel) (*Msg, error) {
	msg := &Msg{
		UUID:  mo.UUID(),
		OrgID: mo.OrgID(),
		Contact: &Contact{
			ID:         mo.ContactID(),
			UUID:       mo.Contact.UUID(),
			Email:      mo.Contact.Email(),
			LastSeenOn: mo.Contact.LastSeenOn(),
			OtherURNs:  otherURNs(mo),
		},
		Text:         mo.Text(),
		Attachments:  mo.Attachments(),
		QuickReplies: mo.QuickReplies(),
		Locale:       mo.Locale(),
		HighPriority: mo.HighPriority(),
		MsgCount:     mo.MsgCount(),
		CreatedOn:    mo.CreatedOn().In(time.UTC),
		ChannelUUID:  ch.UUID(),
		UserID:       mo.CreatedByID(),
		URN:          mo.URN.Identity,
		URNAuth:      string(mo.URN.AuthTokens["default"]),
		IsResend:     mo.IsResend,
		PrevAttempts: mo.ErrorCount(),
	}

	if mo.FlowID() != models.NilFlowID {
		msg.Origin = MsgOriginFlow
		flow, _ := oa.FlowByID(mo.FlowID())
		if flow != nil { // always a chance flow no longer exists
			msg.Flow = &FlowRef{UUID: flow.UUID(), Name: flow.Name()}
		}
	} else if mo.BroadcastID() != models.NilBroadcastID {
		msg.Origin = MsgOriginBroadcast
	} else if mo.TicketUUID() != "" {
		msg.Origin = MsgOriginTicket
	} else {
		msg.Origin = MsgOriginChat
	}

	if mo.Templating() != nil {
		tpl := oa.TemplateByUUID(mo.Templating().Template.UUID)
		if tpl != nil {
			tt := tpl.FindTranslation(ch, mo.Locale())
			if tt != nil {
				msg.Templating = &Templating{
					MsgTemplating: mo.Templating().MsgTemplating,
					Namespace:     tt.Namespace(),
					ExternalID:    tt.ExternalID(),
					Language:      tt.ExternalLocale(), // i.e. en_US
				}
			}
		}
	}

	if mo.ReplyTo != nil {
		msg.ResponseToExternalID = mo.ReplyTo.ExtID
	}
	if mo.Session != nil {
		msg.Session = &Session{
			UUID:       mo.Session.UUID(),
			Status:     sessionStatusMap[mo.Session.Status()],
			SprintUUID: mo.SprintUUID,
		}

		if mo.LastInSprint && mo.WaitTimeout != 0 {
			// This field is set on the last outgoing message in a session's sprint. In the case
			// of the session being at a wait with a timeout then the timeout will be set. It is up to
			// Courier to update the session's timeout appropriately after sending the message.
			msg.Session.Timeout = int(mo.WaitTimeout / time.Second)
		}
	}

	return msg, nil
}

// PushCourierBatch pushes a batch of messages for a single contact and channel onto the appropriate courier queue
func PushCourierBatch(ctx context.Context, rt *runtime.Runtime, vc valkey.Conn, oa *models.OrgAssets, ch *models.Channel, msgs []*models.MsgOut) error {
	batch := make([]*Msg, len(msgs))
	for i, s := range msgs {
		var err error
		batch[i], err = NewCourierMsg(oa, s, ch)
		if err != nil {
			return fmt.Errorf("error creating courier message: %w", err)
		}
	}

	batchJSON := jsonx.MustMarshal(batch)

	return rt.Queues.Courier.Push(ctx, vc, queues.OwnerID(ch.UUID()), ch.TPS(), msgs[0].HighPriority(), batchJSON)
}

// QueueCourierMessages queues messages for a single contact to Courier
func QueueCourierMessages(ctx context.Context, rt *runtime.Runtime, vc valkey.Conn, oa *models.OrgAssets, contactID models.ContactID, channel *models.Channel, msgs []*models.MsgOut) error {
	if len(msgs) == 0 {
		return nil
	}

	// we batch msgs by priority
	batch := make([]*models.MsgOut, 0, len(msgs))

	currentPriority := msgs[0].HighPriority()

	// commits our batch to redis
	commitBatch := func() error {
		if len(batch) > 0 {
			start := time.Now()
			err := PushCourierBatch(ctx, rt, vc, oa, channel, batch)
			if err != nil {
				return err
			}
			slog.Debug("msgs queued to courier", "msgs", len(batch), "contact_id", contactID, "channel_uuid", channel.UUID(), "elapsed", time.Since(start))
		}
		return nil
	}

	for _, m := range msgs {
		// sanity check the state of the msg we're about to queue...
		assert(m.URN != nil, "can't queue a message to courier without a URN")
		assert(m.ContactURNID() != models.NilURNID, "can't queue a message to courier without a URNID")

		// if this msg is the same priority, add to current batch, otherwise start new batch
		if m.HighPriority() == currentPriority {
			batch = append(batch, m)
		} else {
			if err := commitBatch(); err != nil {
				return err
			}

			currentPriority = m.HighPriority()
			batch = []*models.MsgOut{m}
		}
	}

	// any remaining in our batch, queue it up
	return commitBatch()
}

// queues in the layout courier used before, named by channel and TPS, e.g. msgs:<uuid>|10, which may still hold
// messages queued by older versions. A channel can have one for each TPS it's been queued with. Nothing queues to this
// layout any more, so this can go once those queues have drained.
var legacyQueueClearScript = valkey.NewScript(3, `
-- KEYS: [Active, Throttled, Future]
-- ARGV: [ChannelUUID, TPS]
local prefix = "msgs:" .. ARGV[1] .. "|"

local queues = {prefix .. ARGV[2]}
for _, set in ipairs(KEYS) do
  for _, queue in ipairs(redis.call("ZRANGE", set, 0, -1)) do
    if string.sub(queue, 1, #prefix) == prefix then
      table.insert(queues, queue)
    end
  end
end

for _, queue in ipairs(queues) do
  redis.call("DEL", queue .. "/1", queue .. "/0")
end
`)

// ClearCourierQueues clears the courier queues (priority and bulk) for the given channel
func ClearCourierQueues(ctx context.Context, rt *runtime.Runtime, vc valkey.Conn, ch *models.Channel) error {
	if err := rt.Queues.Courier.Clear(ctx, vc, queues.OwnerID(ch.UUID())); err != nil {
		return err
	}
	_, err := legacyQueueClearScript.Do(vc, "msgs:active", "msgs:throttled", "msgs:future", ch.UUID(), ch.TPS())
	return err
}

// see https://github.com/nyaruka/courier/blob/main/attachments.go#L23
type fetchAttachmentRequest struct {
	ChannelType models.ChannelType `json:"channel_type"`
	ChannelUUID assets.ChannelUUID `json:"channel_uuid"`
	URL         string             `json:"url"`
	MsgUUID     events.EventUUID   `json:"msg_uuid"`
}

type fetchAttachmentResponse struct {
	Attachment struct {
		ContentType string `json:"content_type"`
		URL         string `json:"url"`
		Size        int    `json:"size"`
	} `json:"attachment"`
	LogUUID svclogs.UUID `json:"log_uuid"`
}

// FetchAttachment calls courier to fetch the given attachment
func FetchAttachment(ctx context.Context, rt *runtime.Runtime, ch *models.Channel, attURL string, msgUUID events.EventUUID) (utils.Attachment, svclogs.UUID, error) {
	payload := jsonx.MustMarshal(&fetchAttachmentRequest{
		ChannelType: ch.Type(),
		ChannelUUID: ch.UUID(),
		URL:         attURL,
		MsgUUID:     msgUUID,
	})
	req, _ := http.NewRequest("POST", strings.TrimRight(rt.Config.CourierEndpoint, "/")+"/ci/attachment/fetch", bytes.NewReader(payload))
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", rt.Config.CourierAuthToken))

	// this is an internal request to courier whose trace we don't persist, so a plain fetch is enough; the
	// shared services client's 1-minute timeout is big enough that courier determines when things time out
	resp, err := rt.HTTP.Services.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("error calling courier endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("error reading courier response: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("error calling courier endpoint, got non-200 status: %s", string(body))
	}
	fa := &fetchAttachmentResponse{}
	if err := json.Unmarshal(body, fa); err != nil {
		return "", "", fmt.Errorf("error unmarshaling courier response: %w", err)
	}

	return utils.Attachment(fmt.Sprintf("%s:%s", fa.Attachment.ContentType, fa.Attachment.URL)), fa.LogUUID, nil
}
