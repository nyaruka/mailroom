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

const (
	bulkPriority = 0
	highPriority = 1
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

// courier's outgoing queues share this hash tag so that its scripts can use them together in cluster mode. Each channel
// has a queue of high and bulk priority batches ({msgs}:q:<uuid>/1 and /0) and a hash of its state ({msgs}:ch:<uuid>),
// and is either ready to be popped from ({msgs}:ready) or waiting until a time ({msgs}:waiting). Courier owns this
// layout and the scripts here have to match its own, e.g. the push script is courier's utils/queue/lua/push.lua.
const courierKeyBase = "{msgs}"

const (
	courierReadyKey   = courierKeyBase + ":ready"
	courierWaitingKey = courierKeyBase + ":waiting"
)

func courierQueueKey(ch *models.Channel, priority int) string {
	return fmt.Sprintf("%s:q:%s/%d", courierKeyBase, ch.UUID(), priority)
}

func courierChannelKey(ch *models.Channel) string {
	return fmt.Sprintf("%s:ch:%s", courierKeyBase, ch.UUID())
}

var queuePushScript = valkey.NewScript(4, `
-- KEYS: [Queue, Channel, Ready, Waiting]
-- ARGV: [ChannelUUID, TPS, Items]
local queueKey, channelKey, readyKey, waitingKey = KEYS[1], KEYS[2], KEYS[3], KEYS[4]
local channelUUID, tps, items = ARGV[1], tonumber(ARGV[2]), ARGV[3]

local time = redis.call("TIME")
local nowSec = tonumber(time[1])
local now = nowSec + tonumber(time[2]) / 1000000

redis.call("ZADD", queueKey, now, items)

-- the latest push sets the TPS for everything queued for this channel
redis.call("HSET", channelKey, "tps", tps)
redis.call("PERSIST", channelKey)

-- a channel that's used up this second's TPS waits until the next, otherwise make it ready so that these items don't
-- wait on whatever the channel was waiting for
local state = redis.call("HMGET", channelKey, "sec", "used", "inflight")
if tps > 0 and tonumber(state[1]) == nowSec and (tonumber(state[2]) or 0) >= tps then
  if not redis.call("ZSCORE", readyKey, channelUUID) then
    redis.call("ZADD", waitingKey, "LT", nowSec + 1, channelUUID)
  end
  return 0
end

redis.call("ZREM", waitingKey, channelUUID)
redis.call("ZADD", readyKey, tonumber(state[3]) or 0, channelUUID)
return 1
`)

// PushCourierBatch pushes a batch of messages for a single contact and channel onto the appropriate courier queue
func PushCourierBatch(vc valkey.Conn, oa *models.OrgAssets, ch *models.Channel, msgs []*models.MsgOut) error {
	priority := bulkPriority
	if msgs[0].HighPriority() {
		priority = highPriority
	}

	batch := make([]*Msg, len(msgs))
	for i, s := range msgs {
		var err error
		batch[i], err = NewCourierMsg(oa, s, ch)
		if err != nil {
			return fmt.Errorf("error creating courier message: %w", err)
		}
	}

	batchJSON := jsonx.MustMarshal(batch)

	_, err := queuePushScript.Do(vc, courierQueueKey(ch, priority), courierChannelKey(ch), courierReadyKey, courierWaitingKey, ch.UUID(), ch.TPS(), batchJSON)
	return err
}

// QueueCourierMessages queues messages for a single contact to Courier
func QueueCourierMessages(vc valkey.Conn, oa *models.OrgAssets, contactID models.ContactID, channel *models.Channel, msgs []*models.MsgOut) error {
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
			err := PushCourierBatch(vc, oa, channel, batch)
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

var queueClearScript = valkey.NewScript(5, `
-- KEYS: [HighQueue, BulkQueue, Channel, Ready, Waiting]
-- ARGV: [ChannelUUID]
redis.call("DEL", KEYS[1], KEYS[2])
redis.call("ZREM", KEYS[4], ARGV[1])
redis.call("ZREM", KEYS[5], ARGV[1])

-- the channel's state is kept for sends still in flight but with nothing queued it can expire
redis.call("EXPIRE", KEYS[3], 60)
`)

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
func ClearCourierQueues(vc valkey.Conn, ch *models.Channel) error {
	if _, err := queueClearScript.Do(vc, courierQueueKey(ch, highPriority), courierQueueKey(ch, bulkPriority), courierChannelKey(ch), courierReadyKey, courierWaitingKey, ch.UUID()); err != nil {
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
