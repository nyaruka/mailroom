package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/gsm7"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/svclogs"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/v26/core/goflow"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
	"github.com/vinovest/sqlx"
)

func init() {
	goflow.RegisterCheckSendable(func(rt *runtime.Runtime) flows.CheckSendableCallback {
		return func(ctx context.Context, sa flows.SessionAssets, contact *core.Contact, content *core.MsgContent) (core.UnsendableReason, error) {
			return msgCheckSendable(ctx, rt, orgFromAssets(sa), ContactID(contact.ID()), content)
		}
	})
}

// maximum number of repeated messages to same contact allowed in 5 minute window
const msgRepetitionLimit = 20

// MsgID is our internal type for msg ids, which can be null/0
type MsgID int64

// NilMsgID is our constant for a nil msg id
const NilMsgID = MsgID(0)

type Direction string

const (
	DirectionIn  = Direction("I")
	DirectionOut = Direction("O")
)

type MsgVisibility string

const (
	VisibilityVisible         = MsgVisibility("V")
	VisibilityArchived        = MsgVisibility("A")
	VisibilityDeletedByUser   = MsgVisibility("D")
	VisibilityDeletedBySender = MsgVisibility("X")
)

type MsgType string

const (
	MsgTypeText  = MsgType("T")
	MsgTypeVoice = MsgType("V")
)

type MsgStatus string

const (
	MsgStatusPending      = MsgStatus("P") // incoming msg created but not yet handled
	MsgStatusHandled      = MsgStatus("H") // incoming msg handled
	MsgStatusInitializing = MsgStatus("I") // outgoing message that failed to queue
	MsgStatusQueued       = MsgStatus("Q") // outgoing msg created and queued to courier
	MsgStatusWired        = MsgStatus("W") // outgoing msg requested to be sent via channel
	MsgStatusSent         = MsgStatus("S") // outgoing msg having received sent confirmation from channel
	MsgStatusDelivered    = MsgStatus("D") // outgoing msg having received delivery confirmation from channel
	MsgStatusRead         = MsgStatus("R") // outgoing msg having received read confirmation from channel
	MsgStatusErrored      = MsgStatus("E") // outgoing msg which has errored and will be retried
	MsgStatusFailed       = MsgStatus("F") // outgoing msg which has failed permanently
)

// MsgFolder is the denormalized folder that a message belongs to, stored on the message itself so that fetching a
// folder's messages is a single equality. Every message belongs to exactly one folder - the last two codes exist so
// that a null folder column means only "not yet written".
type MsgFolder string

const (
	MsgFolderInbox    = MsgFolder("I") // incoming, visible, handled, no flow
	MsgFolderHandled  = MsgFolder("W") // incoming, visible, handled, has flow
	MsgFolderArchived = MsgFolder("A") // incoming, archived, handled
	MsgFolderOutbox   = MsgFolder("O") // outgoing, visible, initializing/queued/errored
	MsgFolderSent     = MsgFolder("S") // outgoing, visible, wired/sent/delivered/read
	MsgFolderFailed   = MsgFolder("X") // outgoing, visible, failed
	MsgFolderPending  = MsgFolder("P") // incoming, not yet handled
	MsgFolderDeleted  = MsgFolder("D") // deleted by the user or by the sender
)

// DeriveMsgFolder derives the folder that a message belongs to. This is a port of Msg.derive_folder in the main
// codebase and the precedence matters: a message can be archived or deleted whilst still pending, and such messages
// must not appear in the archived folder. Panics for state combinations that shouldn't exist rather than returning
// no folder, because a message without a folder can't be found by folder.
func DeriveMsgFolder(direction Direction, status MsgStatus, visibility MsgVisibility, hasFlow bool) MsgFolder {
	if visibility == VisibilityDeletedByUser || visibility == VisibilityDeletedBySender {
		return MsgFolderDeleted
	}

	if direction == DirectionIn {
		switch status {
		case MsgStatusPending:
			return MsgFolderPending
		case MsgStatusHandled:
			if visibility == VisibilityArchived {
				return MsgFolderArchived
			}
			if hasFlow {
				return MsgFolderHandled
			}
			return MsgFolderInbox
		}
	} else if visibility == VisibilityVisible {
		switch status {
		case MsgStatusInitializing, MsgStatusQueued, MsgStatusErrored:
			return MsgFolderOutbox
		case MsgStatusWired, MsgStatusSent, MsgStatusDelivered, MsgStatusRead:
			return MsgFolderSent
		case MsgStatusFailed:
			return MsgFolderFailed
		}
	}

	panic(fmt.Sprintf("unable to derive folder for msg with direction=%s status=%s visibility=%s", direction, status, visibility))
}

const (
	UnsendableReasonOrgSuspended core.UnsendableReason = "org_suspended"
	UnsendableReasonLooping      core.UnsendableReason = "looping"
)

type MsgFailedReason null.String

const (
	NilMsgFailedReason      = MsgFailedReason("")
	MsgFailedContact        = MsgFailedReason("C") // contact blocked, stopped or archived
	MsgFailedNoDestination  = MsgFailedReason("D")
	MsgFailedSuspended      = MsgFailedReason("S") // workspace suspended
	MsgFailedLooping        = MsgFailedReason("L")
	MsgFailedErrorLimit     = MsgFailedReason("E")
	MsgFailedTooOld         = MsgFailedReason("O")
	MsgFailedChannelRemoved = MsgFailedReason("R")
)

var unsendableToFailedReason = map[core.UnsendableReason]MsgFailedReason{
	core.UnsendableReasonContactBlocked:  MsgFailedContact,
	core.UnsendableReasonContactStopped:  MsgFailedContact,
	core.UnsendableReasonContactArchived: MsgFailedContact,
	core.UnsendableReasonNoRoute:         MsgFailedNoDestination,
	UnsendableReasonOrgSuspended:         MsgFailedSuspended,
	UnsendableReasonLooping:              MsgFailedLooping,
}

// Templating adds db support to the engine's templating struct
type Templating struct {
	*core.MsgTemplating
}

// Scan supports reading templating values from JSON in database
func (t *Templating) Scan(value any) error {
	if value == nil {
		return nil
	}

	b, ok := value.([]byte)
	if !ok {
		return errors.New("failed type assertion to []byte")
	}
	return json.Unmarshal(b, &t)
}

func (t *Templating) Value() (driver.Value, error) {
	if t == nil {
		return nil, nil
	}
	return json.Marshal(t)
}

type MsgInRef struct {
	UUID        events.EventUUID
	ExtID       string
	Attachments []utils.Attachment
	LogUUIDs    []svclogs.UUID
	Handled     bool
}

// Msg is our type for mailroom messages
type Msg struct {
	m struct {
		ID    MsgID            `db:"id"`
		UUID  events.EventUUID `db:"uuid"`
		OrgID OrgID            `db:"org_id"`

		// origin
		BroadcastID BroadcastID `db:"broadcast_id"`
		FlowID      FlowID      `db:"flow_id"`
		TicketUUID  null.String `db:"ticket_uuid"`
		CreatedByID UserID      `db:"created_by_id"`

		// content
		Text         string                   `db:"text"`
		Attachments  pq.StringArray           `db:"attachments"`
		QuickReplies JSONB[[]core.QuickReply] `db:"quickreplies"`
		Locale       i18n.Locale              `db:"locale"`
		Templating   *Templating              `db:"templating"`

		HighPriority       bool          `db:"high_priority"`
		Direction          Direction     `db:"direction"`
		Status             MsgStatus     `db:"status"`
		Visibility         MsgVisibility `db:"visibility"`
		Folder             MsgFolder     `db:"folder"`
		IsAndroid          bool          `db:"is_android"`
		MsgType            MsgType       `db:"msg_type"`
		MsgCount           int           `db:"msg_count"`
		CreatedOn          time.Time     `db:"created_on"`
		ModifiedOn         time.Time     `db:"modified_on"`
		ExternalIdentifier null.String   `db:"external_identifier"`
		ChannelID          ChannelID     `db:"channel_id"`
		ContactID          ContactID     `db:"contact_id"`
		ContactURNID       URNID         `db:"contact_urn_id"`

		SentOn       *time.Time      `db:"sent_on"`
		ErrorCount   int             `db:"error_count"`
		NextAttempt  *time.Time      `db:"next_attempt"`
		FailedReason MsgFailedReason `db:"failed_reason"`
	}
}

func (m *Msg) ID() MsgID              { return m.m.ID }
func (m *Msg) UUID() events.EventUUID { return m.m.UUID }

func (m *Msg) BroadcastID() BroadcastID    { return m.m.BroadcastID }
func (m *Msg) FlowID() FlowID              { return m.m.FlowID }
func (m *Msg) TicketUUID() core.TicketUUID { return core.TicketUUID(m.m.TicketUUID) }
func (m *Msg) CreatedByID() UserID         { return m.m.CreatedByID }

func (m *Msg) Text() string                  { return m.m.Text }
func (m *Msg) Locale() i18n.Locale           { return m.m.Locale }
func (m *Msg) Templating() *Templating       { return m.m.Templating }
func (m *Msg) HighPriority() bool            { return m.m.HighPriority }
func (m *Msg) CreatedOn() time.Time          { return m.m.CreatedOn }
func (m *Msg) ModifiedOn() time.Time         { return m.m.ModifiedOn }
func (m *Msg) SentOn() *time.Time            { return m.m.SentOn }
func (m *Msg) Direction() Direction          { return m.m.Direction }
func (m *Msg) Status() MsgStatus             { return m.m.Status }
func (m *Msg) Visibility() MsgVisibility     { return m.m.Visibility }
func (m *Msg) Folder() MsgFolder             { return m.m.Folder }
func (m *Msg) Type() MsgType                 { return m.m.MsgType }
func (m *Msg) ErrorCount() int               { return m.m.ErrorCount }
func (m *Msg) NextAttempt() *time.Time       { return m.m.NextAttempt }
func (m *Msg) FailedReason() MsgFailedReason { return m.m.FailedReason }
func (m *Msg) ExternalIdentifier() string    { return string(m.m.ExternalIdentifier) }
func (m *Msg) MsgCount() int                 { return m.m.MsgCount }
func (m *Msg) ChannelID() ChannelID          { return m.m.ChannelID }
func (m *Msg) OrgID() OrgID                  { return m.m.OrgID }
func (m *Msg) ContactID() ContactID          { return m.m.ContactID }

func (m *Msg) ContactURNID() URNID         { return m.m.ContactURNID }
func (m *Msg) SetContactURNID(urnID URNID) { m.m.ContactURNID = urnID }

func (m *Msg) SetChannel(channel *Channel) {
	if channel != nil {
		m.m.ChannelID = channel.ID()
		m.m.IsAndroid = channel.IsAndroid()
	} else {
		m.m.ChannelID = NilChannelID
		m.m.IsAndroid = false
	}
}

func (m *Msg) Attachments() []utils.Attachment {
	attachments := make([]utils.Attachment, len(m.m.Attachments))
	for i := range m.m.Attachments {
		attachments[i] = utils.Attachment(m.m.Attachments[i])
	}
	return attachments
}

func (m *Msg) QuickReplies() []core.QuickReply {
	if m.m.QuickReplies.V != nil {
		return m.m.QuickReplies.V
	}
	return []core.QuickReply{}
}

// MsgOut is an outgoing message with the additional information required to queue it
type MsgOut struct {
	*Msg

	URN      *ContactURN // provides URN identity + auth
	Contact  *Contact    // provides contact last seen on
	Session  flows.Session
	ReplyTo  *MsgInRef
	IsResend bool

	// info that courier needs to create a wait timeout fire
	WaitTimeout  time.Duration
	SprintUUID   flows.SprintUUID
	LastInSprint bool
}

// NewIncomingAndroid creates a new incoming message from an Android relayer sync.
func NewIncomingAndroid(orgID OrgID, channelID ChannelID, contactID ContactID, urnID URNID, text string, receivedOn time.Time) *Msg {
	msg := &Msg{}
	m := &msg.m
	m.UUID = events.NewEventUUID()
	m.OrgID = orgID
	m.ChannelID = channelID
	m.ContactID = contactID
	m.ContactURNID = urnID
	m.Text = text
	m.Direction = DirectionIn
	m.Status = MsgStatusPending
	m.Visibility = VisibilityVisible
	m.MsgType = MsgTypeText
	m.IsAndroid = true
	m.CreatedOn = dates.Now()
	m.SentOn = &receivedOn
	return msg
}

// NewIncomingIVR creates a new incoming IVR message for the passed in text and attachment
func NewIncomingIVR(cfg *runtime.Config, orgID OrgID, call *Call, flow *Flow, event *events.MsgReceived) *Msg {
	msg := &Msg{}
	m := &msg.m
	m.UUID = event.UUID()
	m.Text = event.Msg.Text()
	m.Direction = DirectionIn
	m.Status = MsgStatusHandled
	m.Visibility = VisibilityVisible
	m.MsgType = MsgTypeVoice
	m.ContactID = call.ContactID()
	m.ContactURNID = call.ContactURNID()
	m.ChannelID = call.ChannelID()
	m.OrgID = orgID
	m.CreatedOn = event.CreatedOn()

	// add any attachments
	for _, a := range event.Msg.Attachments() {
		m.Attachments = append(m.Attachments, string(NormalizeAttachment(cfg, a)))
	}

	if flow != nil {
		m.FlowID = flow.ID()
	}

	return msg
}

// NewOutgoingIVR creates a new IVR message for the passed in text with the optional attachment
func NewOutgoingIVR(cfg *runtime.Config, orgID OrgID, call *Call, flow *Flow, event *events.IVRCreated) *Msg {
	out := event.Msg
	createdOn := event.CreatedOn()

	msg := &Msg{}
	m := &msg.m
	m.UUID = event.UUID()
	m.OrgID = orgID
	m.Text = out.Text()
	m.Locale = out.Locale()
	m.HighPriority = false
	m.Direction = DirectionOut
	m.Status = MsgStatusWired
	m.Visibility = VisibilityVisible
	m.MsgType = MsgTypeVoice
	m.ContactID = call.ContactID()
	m.ContactURNID = call.ContactURNID()
	m.ChannelID = call.ChannelID()
	m.CreatedOn = createdOn
	m.SentOn = &createdOn

	// if we have attachments, add them
	for _, a := range out.Attachments() {
		m.Attachments = append(m.Attachments, string(NormalizeAttachment(cfg, a)))
	}

	if flow != nil {
		m.FlowID = flow.ID()
	}

	return msg
}

// NewOutgoingFlowMsg creates an outgoing message for the passed in flow message
func NewOutgoingFlowMsg(rt *runtime.Runtime, org *Org, channel *Channel, contact *Contact, flow *Flow, event *events.MsgCreated, replyTo *MsgInRef) (*MsgOut, error) {
	highPriority := replyTo != nil

	return newMsgOut(rt, org, channel, contact, event, flow, NilBroadcastID, NilUserID, replyTo, highPriority)
}

// NewOutgoingBroadcastMsg creates an outgoing message which is part of a broadcast
func NewOutgoingBroadcastMsg(rt *runtime.Runtime, org *Org, channel *Channel, contact *Contact, event *events.MsgCreated, b *Broadcast) (*MsgOut, error) {
	return newMsgOut(rt, org, channel, contact, event, nil, b.ID, b.CreatedByID, nil, false)
}

// NewOutgoingChatMsg creates an outgoing message from chat
func NewOutgoingChatMsg(rt *runtime.Runtime, org *Org, channel *Channel, contact *Contact, event *events.MsgCreated, userID UserID) (*MsgOut, error) {
	return newMsgOut(rt, org, channel, contact, event, nil, NilBroadcastID, userID, nil, true)
}

func newMsgOut(rt *runtime.Runtime, org *Org, channel *Channel, contact *Contact, event *events.MsgCreated, flow *Flow, broadcastID BroadcastID, userID UserID, replyTo *MsgInRef, highPriority bool) (*MsgOut, error) {
	out := event.Msg

	msg := &Msg{}
	m := &msg.m
	m.UUID = event.UUID()
	m.OrgID = org.ID()
	m.ContactID = contact.ID()
	m.BroadcastID = broadcastID
	m.TicketUUID = null.String(event.TicketUUID)
	m.Text = out.Text()
	m.Locale = out.Locale()
	m.QuickReplies = JSONB[[]core.QuickReply]{out.QuickReplies()}
	m.HighPriority = highPriority
	m.Direction = DirectionOut
	m.Status = MsgStatusQueued
	m.Visibility = VisibilityVisible
	m.MsgType = MsgTypeText
	m.MsgCount = 1
	m.CreatedByID = userID
	m.CreatedOn = event.CreatedOn()

	urn := contact.FindURN(out.URN())
	if urn != nil {
		m.ContactURNID = urn.ID
	}
	msg.SetChannel(channel)

	if out.Templating() != nil {
		m.Templating = &Templating{MsgTemplating: out.Templating()}
	}

	// if we have attachments/quick replies, add them
	if len(out.Attachments()) > 0 {
		for _, a := range out.Attachments() {
			m.Attachments = append(m.Attachments, string(NormalizeAttachment(rt.Config, a)))
		}
	}

	if out.UnsendableReason() != "" {
		m.Status = MsgStatusFailed
		m.FailedReason = unsendableToFailedReason[out.UnsendableReason()]
	}

	// if we're sending to a phone, message may have to be sent in multiple parts
	if out.URN().Scheme() == urns.Phone.Prefix {
		m.MsgCount = gsm7.Segments(m.Text) + len(m.Attachments)
	}

	if flow != nil {
		m.FlowID = flow.ID()
	}

	return &MsgOut{Msg: msg, URN: urn, Contact: contact, ReplyTo: replyTo}, nil
}

var msgRepetitionsScript = valkey.NewScript(3, `
local key, contact_id, text = KEYS[1], KEYS[2], KEYS[3]

local msg_key = string.format("%d|%s", contact_id, string.lower(string.sub(text, 1, 128)))
local count = 1

-- try to look up in window
local record = redis.call("HGET", key, msg_key)
if record then
	count = tonumber(record) + 1
end

-- write updated count and set expiration
redis.call("HSET", key, msg_key, count)
redis.call("EXPIRE", key, 300)

return count
`)

// GetMsgRepetitions gets the number of repetitions of this msg text for the given contact in the current 5 minute window
func GetMsgRepetitions(rp *valkey.Pool, contactID ContactID, msg *core.MsgContent) (int, error) {
	vc := rp.Get()
	defer vc.Close()

	keyTime := dates.Now().UTC().Round(time.Minute * 5)
	key := fmt.Sprintf("msg_repetitions:%s", keyTime.Format("2006-01-02T15:04"))
	return valkey.Int(msgRepetitionsScript.Do(vc, key, contactID, msg.Text))
}

var sqlSelectMessagesByUUID = `
SELECT 
	id,
	uuid,
	broadcast_id,
	flow_id,
	ticket_uuid,
	text,
	attachments,
	quickreplies,
	locale,
	templating,
	created_on,
	direction,
	status,
	visibility,
	msg_count,
	error_count,
	next_attempt,
	failed_reason,
	coalesce(high_priority, FALSE) as high_priority,
	external_identifier,
	channel_id,
	contact_id,
	contact_urn_id,
	org_id
FROM
	msgs_msg
WHERE
	org_id = $1 AND
	direction = $2 AND
	uuid = ANY($3)
ORDER BY
	uuid ASC`

// GetMessagesByUUID fetches the messages with the given UUIDs
func GetMessagesByUUID(ctx context.Context, db *sqlx.DB, orgID OrgID, direction Direction, msgUUIDs []events.EventUUID) ([]*Msg, error) {
	return loadMessages(ctx, db, sqlSelectMessagesByUUID, orgID, direction, pq.Array(msgUUIDs))
}

var sqlSelectMessagesForRetry = `
SELECT 
	m.id,
	m.uuid,
	m.broadcast_id,
	m.flow_id,
	m.ticket_uuid,
	m.text,
	m.attachments,
	m.quickreplies,
	m.locale,
	m.templating,
	m.created_on,
	m.direction,
	m.status,
	m.visibility,
	m.msg_count,
	m.error_count,
	m.next_attempt,
	m.failed_reason,
	m.high_priority,
	m.external_identifier,
	m.channel_id,
	m.contact_id,
	m.contact_urn_id,
	m.org_id
FROM
	msgs_msg m
INNER JOIN 
	channels_channel c ON c.id = m.channel_id
WHERE
	m.direction = 'O' AND m.status IN ('I', 'E') AND m.next_attempt <= NOW() AND c.is_active = TRUE
ORDER BY
    m.next_attempt ASC, m.created_on ASC
LIMIT 5000`

// GetMessagesForRetry gets errored outgoing messages scheduled for retry, with an active channel
func GetMessagesForRetry(ctx context.Context, db *sqlx.DB) ([]*Msg, error) {
	return loadMessages(ctx, db, sqlSelectMessagesForRetry)
}

func loadMessages(ctx context.Context, db *sqlx.DB, sql string, params ...any) ([]*Msg, error) {
	rows, err := db.QueryxContext(ctx, sql, params...)
	if err != nil {
		return nil, fmt.Errorf("error querying msgs: %w", err)
	}
	defer rows.Close()

	msgs := make([]*Msg, 0)

	for rows.Next() {
		msg := &Msg{}
		err = rows.StructScan(&msg.m)
		if err != nil {
			return nil, fmt.Errorf("error scanning msg row: %w", err)
		}

		msgs = append(msgs, msg)
	}

	return msgs, nil
}

// NormalizeAttachment will turn any relative URL in the passed in attachment and normalize it to
// include the full host for attachment domains
func NormalizeAttachment(cfg *runtime.Config, attachment utils.Attachment) utils.Attachment {
	// don't try to modify geo type attachments which are just coordinates
	if attachment.ContentType() == "geo" {
		return attachment
	}

	url := attachment.URL()
	if !strings.HasPrefix(url, "http") {
		if strings.HasPrefix(url, "/") {
			url = fmt.Sprintf("https://%s%s", cfg.AttachmentDomain, url)
		} else {
			url = fmt.Sprintf("https://%s/%s", cfg.AttachmentDomain, url)
		}
	}
	return utils.Attachment(fmt.Sprintf("%s:%s", attachment.ContentType(), url))
}

// InsertMessages inserts the passed in messages in a single query
func InsertMessages(ctx context.Context, tx DBorTx, msgs []*Msg) error {
	is := make([]any, len(msgs))
	for i := range msgs {
		m := &msgs[i].m
		m.Folder = DeriveMsgFolder(m.Direction, m.Status, m.Visibility, m.FlowID != NilFlowID)
		is[i] = m
	}

	return BulkQuery(ctx, "insert messages", tx, sqlInsertMsgSQL, is)
}

const sqlInsertMsgSQL = `
INSERT INTO
msgs_msg(uuid, text, attachments, quickreplies, locale, templating, high_priority, created_on, modified_on, sent_on, direction, status,
		 visibility, folder, msg_type, msg_count, error_count, next_attempt, failed_reason, channel_id, is_android,
		 contact_id, contact_urn_id, org_id, flow_id, broadcast_id, ticket_uuid, created_by_id)
  VALUES(:uuid, :text, :attachments, :quickreplies, :locale, :templating, :high_priority, :created_on, now(), :sent_on, :direction, :status,
		 :visibility, :folder, :msg_type, :msg_count, :error_count, :next_attempt, :failed_reason, :channel_id, :is_android,
		 :contact_id, :contact_urn_id, :org_id, :flow_id, :broadcast_id, :ticket_uuid, :created_by_id)
RETURNING id, modified_on`

// MarkMessageHandled updates a message after handling
func MarkMessageHandled(ctx context.Context, tx DBorTx, msgUUID events.EventUUID, status MsgStatus, visibility MsgVisibility, flow *Flow, ticket *Ticket, attachments []utils.Attachment, logUUIDs []svclogs.UUID) error {
	flowID := NilFlowID
	if flow != nil {
		flowID = flow.ID()
	}

	var ticketUUID core.TicketUUID
	if ticket != nil {
		ticketUUID = ticket.UUID
	}

	folder := DeriveMsgFolder(DirectionIn, status, visibility, flowID != NilFlowID)

	// the visibility check leaves alone a message which was deleted whilst it was still waiting to be handled -
	// without it we'd put a message whose content has already been cleared back in the inbox
	_, err := tx.ExecContext(ctx,
		`UPDATE msgs_msg SET status = $2, visibility = $3, folder = $4, flow_id = $5, ticket_uuid = $6, attachments = $7, log_uuids = array_cat(log_uuids, $8) WHERE uuid = $1 AND visibility NOT IN ('D', 'X')`,
		msgUUID, status, visibility, folder, flowID, null.String(ticketUUID), pq.Array(attachments), pq.Array(logUUIDs),
	)
	if err != nil {
		return fmt.Errorf("error marking msg %s as handled: %w", msgUUID, err)
	}
	return nil
}

// MarkMessagesForRequeuing marks the passed in messages as initializing(I) with a next attempt value
// so that the retry messages task will pick them up.
func MarkMessagesForRequeuing(ctx context.Context, db DBorTx, msgs []*Msg) error {
	nextAttempt := time.Now().Add(10 * time.Minute)
	return updateMessageStatus(ctx, db, msgs, MsgStatusInitializing, &nextAttempt)
}

// MarkMessagesQueued marks the passed in messages as queued(Q)
func MarkMessagesQueued(ctx context.Context, db DBorTx, msgs []*Msg) error {
	return updateMessageStatus(ctx, db, msgs, MsgStatusQueued, nil)
}

const sqlUpdateMsgStatus = `
UPDATE msgs_msg
   SET status = m.status, folder = m.folder, next_attempt = m.next_attempt
  FROM (VALUES(:id::bigint, :status, :folder, :next_attempt::timestamptz)) AS m(id, status, folder, next_attempt)
 WHERE msgs_msg.id = m.id`

func updateMessageStatus(ctx context.Context, db DBorTx, msgs []*Msg, status MsgStatus, nextAttempt *time.Time) error {
	is := make([]any, len(msgs))
	for i, msg := range msgs {
		m := &msg.m
		m.Status = status
		m.NextAttempt = nextAttempt
		m.Folder = DeriveMsgFolder(m.Direction, m.Status, m.Visibility, m.FlowID != NilFlowID)
		is[i] = m
	}

	return BulkQuery(ctx, "updating message status", db, sqlUpdateMsgStatus, is)
}

// loads the bare minimum contact info we need for sending messages. Note that contacts may belong to
// different orgs.
const sqlSelectContactsForSending = `
SELECT ROW_TO_JSON(r) FROM (SELECT
	c.id,
	c.uuid,
	c.last_seen_on,
	u.urns AS urns
FROM
	contacts_contact c
LEFT JOIN (
	SELECT contact_id,
		array_agg(
			json_build_object('id', u.id, 'identity', u.identity, 'scheme', u.scheme, 'path', path, 'display', display, 'channel_id', channel_id, 'auth_tokens', auth_tokens) ORDER BY priority DESC, id ASC
		) AS urns
	FROM contacts_contacturn u
	WHERE u.contact_id = ANY($1)
	GROUP BY contact_id
) u ON c.id = u.contact_id
WHERE c.id = ANY($1)
) r`

func loadContactsForSending(ctx context.Context, db *sqlx.DB, contactIDs []ContactID) (map[ContactID]*Contact, error) {
	rows, err := db.QueryContext(ctx, sqlSelectContactsForSending, pq.Array(contactIDs))
	if err != nil {
		return nil, fmt.Errorf("error loading contacts for sending: %w", err)
	}
	defer rows.Close()

	contactsByID := make(map[ContactID]*Contact, len(contactIDs))
	for rows.Next() {
		e := &contactEnvelope{}
		if err := dbutil.ScanJSON(rows, e); err != nil {
			return nil, fmt.Errorf("error scanning contact json: %w", err)
		}

		contactsByID[e.ID] = &Contact{id: e.ID, uuid: e.UUID, lastSeenOn: e.LastSeenOn, urns: e.URNs}
	}

	return contactsByID, nil
}

// PrepareMessagesForRetry prepares messages for retrying by fetching the contact/URN and marking them as QUEUED
func PrepareMessagesForRetry(ctx context.Context, db *sqlx.DB, msgs []*Msg) ([]*MsgOut, error) {
	contactIDs := make([]ContactID, len(msgs))
	urnIDs := make([]URNID, len(msgs))
	for i, m := range msgs {
		contactIDs[i] = m.ContactID()
		urnIDs[i] = m.ContactURNID()
	}

	contactsByID, err := loadContactsForSending(ctx, db, contactIDs)
	if err != nil {
		return nil, fmt.Errorf("error looking up contacts for retries: %w", err)
	}

	cus, err := LoadContactURNs(ctx, db, urnIDs)
	if err != nil {
		return nil, fmt.Errorf("error looking up contact URNs fo retries: %w", err)
	}

	urnsByID := make(map[URNID]*ContactURN, len(cus))
	for _, u := range cus {
		urnsByID[u.ID] = u
	}

	retries := make([]*MsgOut, len(msgs))

	for i, m := range msgs {
		retries[i] = &MsgOut{
			Msg:     m,
			URN:     urnsByID[m.ContactURNID()],
			Contact: contactsByID[m.ContactID()],
		}
	}

	// mark messages as QUEUED
	if err := MarkMessagesQueued(ctx, db, msgs); err != nil {
		return nil, fmt.Errorf("error updating messages for resending: %w", err)
	}

	return retries, nil
}

// the folders here are hardcoded because an outgoing message's folder follows from its status alone - the visibility
// check is what makes that true, by only touching rows which are actually visible
const sqlUpdateMsgForResending = `
UPDATE msgs_msg m
   SET channel_id = r.channel_id, status = 'Q', folder = 'O', error_count = 0, failed_reason = NULL, sent_on = NULL, modified_on = NOW()
  FROM (VALUES(:id::bigint, :channel_id::int)) AS r(id, channel_id)
 WHERE m.id = r.id AND m.visibility = 'V'`

const sqlUpdateMsgResendFailed = `
UPDATE msgs_msg m
   SET channel_id = NULL, status = 'F', folder = 'X', error_count = 0, failed_reason = 'D', sent_on = NULL, modified_on = NOW()
 WHERE id = ANY($1) AND visibility = 'V'`

// PrepareMessagesForResend prepares messages for resending by reselecting a channel and marking them as QUEUED,
// ignoring any which are no longer visible
func PrepareMessagesForResend(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, msgs []*Msg) ([]*MsgOut, error) {
	channels := oa.SessionAssets().Channels()

	contactIDs := make([]ContactID, len(msgs))
	for i, m := range msgs {
		contactIDs[i] = m.ContactID()
	}

	contactsByID, err := loadContactsForSending(ctx, rt.DB, contactIDs)
	if err != nil {
		return nil, fmt.Errorf("error looking up contacts for retries: %w", err)
	}

	// for the bulk db updates
	resends := make([]any, 0, len(msgs))
	refails := make([]MsgID, 0, len(msgs))

	resent := make([]*MsgOut, 0, len(msgs))

	for _, msg := range msgs {
		// ignore messages which aren't visible, i.e. have been deleted since they were loaded
		if msg.m.Visibility != VisibilityVisible {
			continue
		}

		urnID := msg.ContactURNID()
		var ch *Channel
		var cu *ContactURN

		if urnID != NilURNID {
			var err error

			// reselect channel for this message's URN
			cu, err = LoadContactURN(ctx, rt.DB, urnID)
			if err != nil {
				return nil, fmt.Errorf("error loading URN: %w", err)
			}

			urn, _ := cu.Encode(oa)
			fu, err := core.ParseURN(channels, urn, assets.IgnoreMissing)
			if err != nil {
				return nil, fmt.Errorf("error parsing URN: %w", err)
			}

			if fch := channels.GetForURN(fu, assets.ChannelRoleSend); fch != nil {
				ch = oa.ChannelByUUID(fch.UUID())
			}
		}

		if ch != nil {
			msg.m.ChannelID = ch.ID()
			msg.m.Status = MsgStatusQueued
			msg.m.Folder = MsgFolderOutbox
			msg.m.SentOn = nil
			msg.m.ErrorCount = 0
			msg.m.FailedReason = ""

			resends = append(resends, msg.m)
			resent = append(resent, &MsgOut{
				Msg:      msg,
				URN:      cu,
				Contact:  contactsByID[msg.m.ContactID],
				IsResend: true,
			})
		} else {
			// if we don't have channel or a URN, fail again
			msg.m.ChannelID = NilChannelID
			msg.m.Status = MsgStatusFailed
			msg.m.Folder = MsgFolderFailed
			msg.m.SentOn = nil
			msg.m.ErrorCount = 0
			msg.m.FailedReason = MsgFailedNoDestination

			refails = append(refails, MsgID(msg.m.ID))
		}
	}

	// update the messages that can be resent
	if err := BulkQuery(ctx, "updating messages for resending", rt.DB, sqlUpdateMsgForResending, resends); err != nil {
		return nil, fmt.Errorf("error updating messages for resending: %w", err)
	}

	// and update the messages that can't be
	_, err = rt.DB.ExecContext(ctx, sqlUpdateMsgResendFailed, pq.Array(refails))
	if err != nil {
		return nil, fmt.Errorf("error updating non-resendable messages: %w", err)
	}

	return resent, nil
}

// like sqlUpdateMsgForResending the failed folder is hardcoded because it follows from the status alone for a
// visible outgoing message, which is what the visibility check restricts this to
const sqlFailChannelMessages = `
WITH rows AS (
	SELECT id FROM msgs_msg
	WHERE org_id = $1 AND direction = 'O' AND channel_id = $2 AND status IN ('I', 'Q', 'E') AND visibility = 'V'
	LIMIT 1000
)
UPDATE msgs_msg SET status = 'F', folder = 'X', failed_reason = $3, modified_on = NOW() WHERE id IN (SELECT id FROM rows)`

func FailChannelMessages(ctx context.Context, db *sql.DB, orgID OrgID, channelID ChannelID, failedReason MsgFailedReason) error {
	for {
		// and update the messages as FAILED
		res, err := db.ExecContext(ctx, sqlFailChannelMessages, orgID, channelID, failedReason)
		if err != nil {
			return err
		}
		rows, _ := res.RowsAffected()
		if rows == 0 {
			break
		}
	}
	return nil
}

// the WHERE on the update repeats the status and visibility checks from the CTE so that a message which was sent,
// failed or retried between the two can't be clobbered. The join to contacts_contact is only for the contact UUID
// needed by the event tags - msgs_msg.contact_id is a non-null protected FK so it never excludes a row, which is
// what lets callers loop until this returns nothing.
const sqlFailOldAndroidMessages = `
WITH rows AS (
	SELECT id FROM msgs_msg
	WHERE direction = 'O' AND is_android = TRUE AND status IN ('I', 'Q', 'E') AND visibility = 'V' AND created_on <= $1
	LIMIT $2
)
   UPDATE msgs_msg SET status = 'F', folder = $3, failed_reason = $4, modified_on = NOW()
     FROM rows, contacts_contact c
    WHERE msgs_msg.id = rows.id AND msgs_msg.status IN ('I', 'Q', 'E') AND msgs_msg.visibility = 'V' AND c.id = msgs_msg.contact_id
RETURNING msgs_msg.org_id AS org_id, msgs_msg.uuid AS msg_uuid, c.uuid AS contact_uuid`

// FailOldAndroidMessages fails up to limit outgoing Android messages created on or before the given time which are
// still waiting to be sent, i.e. their relayer hasn't synced and they'd otherwise sit in the outbox forever. Only
// Android messages are failed this way because messages on other channel types are still in courier's queue.
//
// It returns an event tag recording the change for each message failed (to be queued to the history table), so
// callers should keep calling until it returns nothing.
func FailOldAndroidMessages(ctx context.Context, db DBorTx, olderThan time.Time, limit int) ([]*EventTag, error) {
	// the query only touches visible messages, so the failed folder follows from the status alone
	folder := DeriveMsgFolder(DirectionOut, MsgStatusFailed, VisibilityVisible, false)

	rows := []*struct {
		OrgID       OrgID            `db:"org_id"`
		MsgUUID     events.EventUUID `db:"msg_uuid"`
		ContactUUID core.ContactUUID `db:"contact_uuid"`
	}{}

	if err := db.SelectContext(ctx, &rows, sqlFailOldAndroidMessages, olderThan, limit, folder, MsgFailedTooOld); err != nil {
		return nil, fmt.Errorf("error failing old android messages: %w", err)
	}

	tags := make([]*EventTag, len(rows))
	for i, r := range rows {
		tags[i] = NewMsgStatusTag(r.OrgID, r.ContactUUID, r.MsgUUID, MsgStatusFailed, MsgFailedTooOld)
	}

	return tags, nil
}

// AndroidStatusUpdate is a status change reported by an Android relayer during a sync, for one of the outgoing
// messages it was asked to send.
type AndroidStatusUpdate struct {
	MsgUUID events.EventUUID
	Status  MsgStatus

	// the time to record as the message's sent_on, or nil to leave it as it is
	SentOn *time.Time

	// whether SentOn replaces an existing sent_on, rather than only filling in a missing one
	OverwriteSentOn bool
}

// like sqlFailOldAndroidMessages the join to contacts_contact is only for the contact UUID needed by the event tags.
// The direction and visibility checks are in the WHERE rather than only in the caller so that a message which changed
// after we read it can't be given a folder that doesn't match its actual state.
const sqlUpdateAndroidMsgStatuses = `
   UPDATE msgs_msg
      SET status = u.status, folder = u.folder, modified_on = NOW(),
          sent_on = CASE WHEN u.overwrite_sent_on THEN u.sent_on ELSE COALESCE(msgs_msg.sent_on, u.sent_on) END
     FROM UNNEST($2::uuid[], $3::text[], $4::text[], $5::timestamptz[], $6::bool[]) AS u(uuid, status, folder, sent_on, overwrite_sent_on), contacts_contact c
    WHERE msgs_msg.uuid = u.uuid AND msgs_msg.org_id = $1 AND msgs_msg.direction = 'O' AND msgs_msg.visibility = 'V' AND c.id = msgs_msg.contact_id
RETURNING msgs_msg.uuid AS msg_uuid, msgs_msg.status AS status, c.uuid AS contact_uuid`

// UpdateAndroidMessageStatuses applies status changes reported by an Android relayer to the given org's messages,
// ignoring any which aren't outgoing and visible. Because they're all applied in a single statement, two updates for
// the same message would silently overwrite each other, so callers have to fold those into one first.
//
// It returns an event tag recording the change for each message actually updated, to be queued to the history table
// by the caller, because nothing else records these transitions.
func UpdateAndroidMessageStatuses(ctx context.Context, db DBorTx, orgID OrgID, updates []*AndroidStatusUpdate) ([]*EventTag, error) {
	if len(updates) == 0 {
		return nil, nil
	}

	uuids := make([]events.EventUUID, len(updates))
	statuses := make([]MsgStatus, len(updates))
	folders := make([]MsgFolder, len(updates))
	sentOns := make([]*time.Time, len(updates))
	overwriteSentOns := make([]bool, len(updates))
	seen := make(map[events.EventUUID]bool, len(updates))

	for i, u := range updates {
		if seen[u.MsgUUID] {
			return nil, fmt.Errorf("more than one update for message %s", u.MsgUUID)
		}
		seen[u.MsgUUID] = true

		uuids[i] = u.MsgUUID
		statuses[i] = u.Status
		// the folder of an outgoing message follows from its status alone, and the WHERE only touches rows which are
		// actually outgoing and visible
		folders[i] = DeriveMsgFolder(DirectionOut, u.Status, VisibilityVisible, false)
		sentOns[i] = u.SentOn
		// there's nothing to overwrite sent_on with if there's no new value, and clearing it would break the
		// constraint that a sent message has a sent_on
		overwriteSentOns[i] = u.OverwriteSentOn && u.SentOn != nil
	}

	rows := []*struct {
		MsgUUID     events.EventUUID `db:"msg_uuid"`
		Status      MsgStatus        `db:"status"`
		ContactUUID core.ContactUUID `db:"contact_uuid"`
	}{}

	err := db.SelectContext(ctx, &rows, sqlUpdateAndroidMsgStatuses, orgID,
		pq.Array(uuids), pq.Array(statuses), pq.Array(folders), pq.Array(sentOns), pq.Array(overwriteSentOns),
	)
	if err != nil {
		return nil, fmt.Errorf("error updating android message statuses: %w", err)
	}

	tags := make([]*EventTag, len(rows))
	for i, r := range rows {
		tags[i] = NewMsgStatusTag(orgID, r.ContactUUID, r.MsgUUID, r.Status, NilMsgFailedReason)
	}

	return tags, nil
}

// AndroidOutboxMsg is a queued outgoing message being offered to a channel's relayer, with the phone number to send
// it to.
type AndroidOutboxMsg struct {
	ID    MsgID  `db:"id"`
	Text  string `db:"text"`
	Phone string `db:"phone"`
}

// the relayer is given messages that are still queued, i.e. not yet claimed as sent by any earlier sync. Messages
// without a URN can't be sent by a relayer at all so they're excluded rather than offered and silently dropped.
const sqlSelectAndroidOutbox = `
    SELECT m.id, m.text, u.path AS phone
      FROM msgs_msg m
INNER JOIN contacts_contacturn u ON u.id = m.contact_urn_id
     WHERE m.channel_id = $1 AND m.direction = 'O' AND m.status = 'Q' AND NOT (m.id = ANY($2))
  ORDER BY m.created_on ASC
     LIMIT $3`

// GetAndroidOutbox returns the queued outgoing messages to offer to a channel's relayer, oldest first, excluding any
// the relayer has told us it already holds.
func GetAndroidOutbox(ctx context.Context, db DBorTx, channelID ChannelID, exclude []MsgID, limit int) ([]*AndroidOutboxMsg, error) {
	msgs := []*AndroidOutboxMsg{}

	// a nil slice becomes a NULL array, and everything compared against that is NULL rather than excluded
	if exclude == nil {
		exclude = []MsgID{}
	}

	if err := db.SelectContext(ctx, &msgs, sqlSelectAndroidOutbox, channelID, pq.Array(exclude), limit); err != nil {
		return nil, fmt.Errorf("error selecting android outbox messages: %w", err)
	}

	return msgs, nil
}

// CreateMsgOut creates a new outgoing message to the given contact, resolving the destination etc
func CreateMsgOut(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, c *core.Contact, content *core.MsgContent, templateID TemplateID, templateVariables []string, locale i18n.Locale, expressionsContext *types.XObject) (*core.MsgOut, error) {
	// resolve URN + channel for this contact
	urn := urns.NilURN
	var channel *Channel
	var channelRef *assets.ChannelReference
	if r := c.ResolveRoute(); r != nil {
		urn = r.URN
		channel = oa.ChannelByUUID(r.Channel.UUID())
		channelRef = r.Channel.Reference()
	}

	// if there's an expressions context, evaluate text etc
	if expressionsContext != nil {
		ev := goflow.Engine(rt).Evaluator()

		content.Text, _, _ = ev.Template(ctx, oa.Env(), expressionsContext, content.Text, nil)
		templateVariables = slices.Clone(templateVariables)

		for i := range content.Attachments {
			evaluated, _, _ := ev.Template(ctx, oa.Env(), expressionsContext, string(content.Attachments[i]), nil)
			content.Attachments[i] = utils.Attachment(evaluated)
		}
		for i := range content.QuickReplies {
			content.QuickReplies[i].Text, _, _ = ev.Template(ctx, oa.Env(), expressionsContext, content.QuickReplies[i].Text, nil)
		}
		for i := range templateVariables {
			templateVariables[i], _, _ = ev.Template(ctx, oa.Env(), expressionsContext, templateVariables[i], nil)
		}
	}

	// if we have a template, try to generate templating
	var templating *core.MsgTemplating
	if templateID != NilTemplateID && channel != nil {
		template := oa.TemplateByID(templateID)
		if template != nil {
			flowTemplate := core.NewTemplate(template)
			flowChannel := core.NewChannel(channel)

			// look for a translation in the contact's locale, or the org's default locale
			locales := make([]i18n.Locale, 0, 2)
			if c.Language() != "" {
				locales = append(locales, c.Locale(oa.Env()))
			}
			locales = append(locales, oa.Env().DefaultLocale())

			trans := flowTemplate.FindTranslation(flowChannel, locales)
			if trans != nil {
				translation := core.NewTemplateTranslation(trans)
				templating = core.NewTemplate(template).Templating(translation, templateVariables)

				// override message content to be a preview of template message and override locale to match the template translation
				content = translation.Preview(templating.Variables)
				locale = translation.Locale()
			}
		}
	}

	// is this message sendable?
	var unsendableReason core.UnsendableReason
	if c.Status() == core.ContactStatusBlocked {
		unsendableReason = core.UnsendableReasonContactBlocked
	} else if c.Status() == core.ContactStatusStopped {
		unsendableReason = core.UnsendableReasonContactStopped
	} else if c.Status() == core.ContactStatusArchived {
		unsendableReason = core.UnsendableReasonContactArchived
	} else if urn == urns.NilURN || channel == nil {
		unsendableReason = core.UnsendableReasonNoRoute
	} else {
		var err error
		unsendableReason, err = msgCheckSendable(ctx, rt, oa.Org(), ContactID(c.ID()), content)
		if err != nil {
			return nil, fmt.Errorf("error checking if message is sendable: %w", err)
		}
	}

	return core.NewMsgOut(urn, channelRef, content, templating, locale, unsendableReason), nil
}

// the from_visibility check is what makes this safe against a message being deleted between being loaded and being
// updated here - without it we'd resurrect a message whose content has already been cleared
const sqlUpdateMsgVisibility = `
UPDATE msgs_msg
   SET visibility = m.visibility, folder = m.folder, modified_on = NOW()
  FROM (VALUES(:id::bigint, :visibility, :folder, :from_visibility)) AS m(id, visibility, folder, from_visibility)
 WHERE msgs_msg.id = m.id AND msgs_msg.visibility = m.from_visibility`

type msgVisibilityUpdate struct {
	ID             MsgID         `db:"id"`
	Visibility     MsgVisibility `db:"visibility"`
	Folder         MsgFolder     `db:"folder"`
	FromVisibility MsgVisibility `db:"from_visibility"`
}

// ArchiveMessages archives the given incoming messages, ignoring any that aren't currently visible
func ArchiveMessages(ctx context.Context, db DBorTx, msgs []*Msg) error {
	return updateMessageVisibility(ctx, db, msgs, VisibilityVisible, VisibilityArchived)
}

// RestoreMessages un-archives the given incoming messages, ignoring any that aren't currently archived
func RestoreMessages(ctx context.Context, db DBorTx, msgs []*Msg) error {
	return updateMessageVisibility(ctx, db, msgs, VisibilityArchived, VisibilityVisible)
}

func updateMessageVisibility(ctx context.Context, db DBorTx, msgs []*Msg, from, to MsgVisibility) error {
	updates := make([]*msgVisibilityUpdate, 0, len(msgs))

	for _, msg := range msgs {
		m := &msg.m

		// ignore messages that aren't in the visibility we're transitioning from, which includes deleted messages
		if m.Visibility != from {
			continue
		}

		updates = append(updates, &msgVisibilityUpdate{
			ID:             m.ID,
			Visibility:     to,
			Folder:         DeriveMsgFolder(m.Direction, m.Status, to, m.FlowID != NilFlowID),
			FromVisibility: from,
		})
	}

	return BulkQuery(ctx, "updating message visibility", db, sqlUpdateMsgVisibility, updates)
}

const sqlUpdateMsgDeleted = `
   UPDATE msgs_msg
      SET visibility = $3, folder = 'D', text = '', attachments = '{}'
    WHERE org_id = $1 AND uuid = ANY($2) AND direction = 'I' AND visibility IN ('V', 'A')
RETURNING id`

func DeleteMessages(ctx context.Context, tx *sqlx.Tx, orgID OrgID, uuids []events.EventUUID, visibility MsgVisibility) error {
	ids := make([]MsgID, 0, len(uuids))

	if err := tx.SelectContext(ctx, &ids, sqlUpdateMsgDeleted, orgID, pq.Array(uuids), visibility); err != nil {
		return fmt.Errorf("error updating message visibility: %w", err)
	}

	_, err := tx.ExecContext(ctx, `DELETE FROM msgs_msg_labels WHERE msg_id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("error clearing message labels from deleted messages: %w", err)
	}

	return nil
}

func msgCheckSendable(ctx context.Context, rt *runtime.Runtime, org *Org, contactID ContactID, content *core.MsgContent) (core.UnsendableReason, error) {
	if org.Suspended() {
		return UnsendableReasonOrgSuspended, nil
	}

	// does this look like a message loop?
	repetitions, err := GetMsgRepetitions(rt.VK, contactID, content)
	if err != nil {
		return "", fmt.Errorf("error looking up msg repetitions: %w", err)
	}
	if repetitions > msgRepetitionLimit {
		slog.Warn("too many repetitions, failing message", "contact_id", contactID, "text", content.Text, "repetitions", repetitions)

		return UnsendableReasonLooping, nil
	}

	return "", nil
}

// NilID implementations

func (i *MsgID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i MsgID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *MsgID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i MsgID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

func (i *BroadcastID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i BroadcastID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *BroadcastID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i BroadcastID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

func (s MsgFailedReason) Value() (driver.Value, error) { return null.StringValue(s) }
func (s *MsgFailedReason) Scan(value any) error        { return null.ScanString(value, s) }
