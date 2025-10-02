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

	"github.com/gomodule/redigo/redis"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/gsm7"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/utils/clogs"
	"github.com/nyaruka/null/v3"
)

func init() {
	goflow.RegisterCheckSendable(func(rt *runtime.Runtime) flows.CheckSendableCallback {
		return func(sa flows.SessionAssets, contact *flows.Contact, content *flows.MsgContent) (flows.UnsendableReason, error) {
			return msgCheckSendable(rt, orgFromAssets(sa), contact, content)
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
	MsgTypeOptIn = MsgType("O")
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

const (
	UnsendableReasonOrgStatus flows.UnsendableReason = "org_status"
	UnsendableReasonLooping   flows.UnsendableReason = "looping"
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

var unsendableToFailedReason = map[flows.UnsendableReason]MsgFailedReason{
	flows.UnsendableReasonContactStatus: MsgFailedContact,
	flows.UnsendableReasonNoDestination: MsgFailedNoDestination,
	UnsendableReasonOrgStatus:           MsgFailedSuspended,
	UnsendableReasonLooping:             MsgFailedLooping,
}

// Templating adds db support to the engine's templating struct
type Templating struct {
	*flows.MsgTemplating
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
	ID          MsgID
	ExtID       string
	Attachments []utils.Attachment
	LogUUIDs    []clogs.UUID
	Handled     bool
}

// Msg is our type for mailroom messages
type Msg struct {
	m struct {
		ID    MsgID           `db:"id"`
		UUID  flows.EventUUID `db:"uuid"`
		OrgID OrgID           `db:"org_id"`

		// origin
		BroadcastID BroadcastID `db:"broadcast_id"`
		FlowID      FlowID      `db:"flow_id"`
		TicketUUID  null.String `db:"ticket_uuid"`
		CreatedByID UserID      `db:"created_by_id"`

		// content
		Text         string         `db:"text"`
		Attachments  pq.StringArray `db:"attachments"`
		QuickReplies pq.StringArray `db:"quick_replies"`
		OptInID      OptInID        `db:"optin_id"`
		Locale       i18n.Locale    `db:"locale"`
		Templating   *Templating    `db:"templating"`

		HighPriority bool          `db:"high_priority"`
		Direction    Direction     `db:"direction"`
		Status       MsgStatus     `db:"status"`
		Visibility   MsgVisibility `db:"visibility"`
		IsAndroid    bool          `db:"is_android"`
		MsgType      MsgType       `db:"msg_type"`
		MsgCount     int           `db:"msg_count"`
		CreatedOn    time.Time     `db:"created_on"`
		ModifiedOn   time.Time     `db:"modified_on"`
		ExternalID   null.String   `db:"external_id"`
		ChannelID    ChannelID     `db:"channel_id"`
		ContactID    ContactID     `db:"contact_id"`
		ContactURNID URNID         `db:"contact_urn_id"`

		SentOn       *time.Time      `db:"sent_on"`
		ErrorCount   int             `db:"error_count"`
		NextAttempt  *time.Time      `db:"next_attempt"`
		FailedReason MsgFailedReason `db:"failed_reason"`
	}
}

func (m *Msg) ID() MsgID             { return m.m.ID }
func (m *Msg) UUID() flows.EventUUID { return m.m.UUID }

func (m *Msg) BroadcastID() BroadcastID     { return m.m.BroadcastID }
func (m *Msg) FlowID() FlowID               { return m.m.FlowID }
func (m *Msg) TicketUUID() flows.TicketUUID { return flows.TicketUUID(m.m.TicketUUID) }
func (m *Msg) CreatedByID() UserID          { return m.m.CreatedByID }

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
func (m *Msg) Type() MsgType                 { return m.m.MsgType }
func (m *Msg) ErrorCount() int               { return m.m.ErrorCount }
func (m *Msg) NextAttempt() *time.Time       { return m.m.NextAttempt }
func (m *Msg) FailedReason() MsgFailedReason { return m.m.FailedReason }
func (m *Msg) ExternalID() string            { return string(m.m.ExternalID) }
func (m *Msg) MsgCount() int                 { return m.m.MsgCount }
func (m *Msg) ChannelID() ChannelID          { return m.m.ChannelID }
func (m *Msg) OrgID() OrgID                  { return m.m.OrgID }

func (m *Msg) OptInID() OptInID     { return m.m.OptInID }
func (m *Msg) ContactID() ContactID { return m.m.ContactID }
func (m *Msg) ContactURNID() URNID  { return m.m.ContactURNID }

func (m *Msg) SetChannel(channel *Channel) {
	if channel != nil {
		m.m.ChannelID = channel.ID()
		m.m.IsAndroid = channel.IsAndroid()
	} else {
		m.m.ChannelID = NilChannelID
		m.m.IsAndroid = false
	}
}

func (m *Msg) SetURN(urn urns.URN) {
	m.m.ContactURNID = NilURNID

	// try to extract id as param
	if urn != urns.NilURN {
		if id := GetURNInt(urn, "id"); id != 0 {
			m.m.ContactURNID = URNID(id)
		}
	}
}

func (m *Msg) Attachments() []utils.Attachment {
	attachments := make([]utils.Attachment, len(m.m.Attachments))
	for i := range m.m.Attachments {
		attachments[i] = utils.Attachment(m.m.Attachments[i])
	}
	return attachments
}

func (m *Msg) QuickReplies() []flows.QuickReply {
	qrs := make([]flows.QuickReply, len(m.m.QuickReplies))
	for i, mqr := range m.m.QuickReplies {
		qr := flows.QuickReply{}
		qr.UnmarshalText([]byte(mqr))
		qrs[i] = qr
	}
	return qrs
}

// MsgOut is an outgoing message with the additional information required to queue it
type MsgOut struct {
	*Msg

	URN      *ContactURN    // provides URN identity + auth
	Contact  *flows.Contact // provides contact last seen on
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
	m.UUID = flows.NewEventUUID()
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

// NewOutgoingOptInMsg creates an outgoing optin message
func NewOutgoingOptInMsg(rt *runtime.Runtime, orgID OrgID, contact *flows.Contact, flow *Flow, optIn *OptIn, channel *Channel, event *events.OptInRequested, replyTo *MsgInRef) *MsgOut {
	msg := &Msg{}
	m := &msg.m
	m.UUID = event.UUID()
	m.OrgID = orgID
	m.ContactID = ContactID(contact.ID())
	m.HighPriority = replyTo != nil
	m.Direction = DirectionOut
	m.Status = MsgStatusQueued
	m.Visibility = VisibilityVisible
	m.MsgType = MsgTypeOptIn
	m.MsgCount = 1
	m.CreatedOn = event.CreatedOn()

	msg.SetChannel(channel)
	msg.SetURN(event.URN)

	if flow != nil {
		m.FlowID = flow.ID()
	}
	if optIn != nil {
		m.OptInID = optIn.ID()
	}

	return &MsgOut{Msg: msg, Contact: contact, ReplyTo: replyTo}
}

// NewOutgoingFlowMsg creates an outgoing message for the passed in flow message
func NewOutgoingFlowMsg(rt *runtime.Runtime, org *Org, channel *Channel, contact *flows.Contact, flow *Flow, event *events.MsgCreated, replyTo *MsgInRef) (*MsgOut, error) {
	highPriority := replyTo != nil

	return newMsgOut(rt, org, channel, contact, event, flow, NilBroadcastID, NilOptInID, NilUserID, replyTo, highPriority)
}

// NewOutgoingBroadcastMsg creates an outgoing message which is part of a broadcast
func NewOutgoingBroadcastMsg(rt *runtime.Runtime, org *Org, channel *Channel, contact *flows.Contact, event *events.MsgCreated, b *Broadcast) (*MsgOut, error) {
	return newMsgOut(rt, org, channel, contact, event, nil, b.ID, b.OptInID, b.CreatedByID, nil, false)
}

// NewOutgoingChatMsg creates an outgoing message from chat
func NewOutgoingChatMsg(rt *runtime.Runtime, org *Org, channel *Channel, contact *flows.Contact, event *events.MsgCreated, userID UserID) (*MsgOut, error) {
	return newMsgOut(rt, org, channel, contact, event, nil, NilBroadcastID, NilOptInID, userID, nil, true)
}

func newMsgOut(rt *runtime.Runtime, org *Org, channel *Channel, contact *flows.Contact, event *events.MsgCreated, flow *Flow, broadcastID BroadcastID, optInID OptInID, userID UserID, replyTo *MsgInRef, highPriority bool) (*MsgOut, error) {
	out := event.Msg

	msg := &Msg{}
	m := &msg.m
	m.UUID = event.UUID()
	m.OrgID = org.ID()
	m.ContactID = ContactID(contact.ID())
	m.BroadcastID = broadcastID
	m.TicketUUID = null.String(event.TicketUUID)
	m.Text = out.Text()
	m.Locale = out.Locale()
	m.OptInID = optInID
	m.HighPriority = highPriority
	m.Direction = DirectionOut
	m.Status = MsgStatusQueued
	m.Visibility = VisibilityVisible
	m.MsgType = MsgTypeText
	m.MsgCount = 1
	m.CreatedByID = userID
	m.CreatedOn = event.CreatedOn()

	msg.SetChannel(channel)
	msg.SetURN(out.URN())

	if out.Templating() != nil {
		m.Templating = &Templating{MsgTemplating: out.Templating()}
	}

	// if we have attachments/quick replies, add them
	if len(out.Attachments()) > 0 {
		for _, a := range out.Attachments() {
			m.Attachments = append(m.Attachments, string(NormalizeAttachment(rt.Config, a)))
		}
	}
	if len(out.QuickReplies()) > 0 {
		for _, qr := range out.QuickReplies() {
			mqr, _ := qr.MarshalText()
			m.QuickReplies = append(m.QuickReplies, string(mqr))
		}
	}

	if out.UnsendableReason() != flows.NilUnsendableReason {
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

	return &MsgOut{Msg: msg, Contact: contact, ReplyTo: replyTo}, nil
}

var msgRepetitionsScript = redis.NewScript(3, `
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
func GetMsgRepetitions(rp *redis.Pool, contact *flows.Contact, msg *flows.MsgContent) (int, error) {
	vc := rp.Get()
	defer vc.Close()

	keyTime := dates.Now().UTC().Round(time.Minute * 5)
	key := fmt.Sprintf("msg_repetitions:%s", keyTime.Format("2006-01-02T15:04"))
	return redis.Int(msgRepetitionsScript.Do(vc, key, contact.ID(), msg.Text))
}

var sqlSelectMessagesByID = `
SELECT 
	id,
	uuid,	
	broadcast_id,
	flow_id,
	ticket_uuid,
	optin_id,
	text,
	attachments,
	quick_replies,
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
	external_id,
	channel_id,
	contact_id,
	contact_urn_id,
	org_id
FROM
	msgs_msg
WHERE
	org_id = $1 AND
	direction = $2 AND
	id = ANY($3)
ORDER BY
	id ASC`

// GetMessagesByID fetches the messages with the given ids
func GetMessagesByID(ctx context.Context, db *sqlx.DB, orgID OrgID, direction Direction, msgIDs []MsgID) ([]*Msg, error) {
	return loadMessages(ctx, db, sqlSelectMessagesByID, orgID, direction, pq.Array(msgIDs))
}

var sqlSelectMessagesForRetry = `
SELECT 
	m.id,
	m.uuid,
	m.broadcast_id,
	m.flow_id,
	m.ticket_uuid,
	m.optin_id,
	m.text,
	m.attachments,
	m.quick_replies,
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
	m.external_id,
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
		is[i] = &msgs[i].m
	}

	return BulkQuery(ctx, "insert messages", tx, sqlInsertMsgSQL, is)
}

const sqlInsertMsgSQL = `
INSERT INTO
msgs_msg(uuid, text, attachments, quick_replies, locale, templating, high_priority, created_on, modified_on, sent_on, direction, status,
		 visibility, msg_type, msg_count, error_count, next_attempt, failed_reason, channel_id, is_android,
		 contact_id, contact_urn_id, org_id, flow_id, broadcast_id, ticket_uuid, optin_id, created_by_id)
  VALUES(:uuid, :text, :attachments, :quick_replies, :locale, :templating, :high_priority, :created_on, now(), :sent_on, :direction, :status,
		 :visibility, :msg_type, :msg_count, :error_count, :next_attempt, :failed_reason, :channel_id, :is_android,
		 :contact_id, :contact_urn_id, :org_id, :flow_id, :broadcast_id, :ticket_uuid, :optin_id, :created_by_id)
RETURNING id, modified_on`

// MarkMessageHandled updates a message after handling
func MarkMessageHandled(ctx context.Context, tx DBorTx, msgID MsgID, status MsgStatus, visibility MsgVisibility, flow *Flow, ticket *Ticket, attachments []utils.Attachment, logUUIDs []clogs.UUID) error {
	flowID := NilFlowID
	if flow != nil {
		flowID = flow.ID()
	}

	var ticketUUID flows.TicketUUID
	if ticket != nil {
		ticketUUID = ticket.UUID
	}

	_, err := tx.ExecContext(ctx,
		`UPDATE msgs_msg SET status = $2, visibility = $3, flow_id = $4, ticket_uuid = $5, attachments = $6, log_uuids = array_cat(log_uuids, $7) WHERE id = $1`,
		msgID, status, visibility, flowID, null.String(ticketUUID), pq.Array(attachments), pq.Array(logUUIDs),
	)
	if err != nil {
		return fmt.Errorf("error marking msg #%d as handled: %w", msgID, err)
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
   SET status = m.status, next_attempt = m.next_attempt::timestamptz
  FROM (VALUES(:id, :status, :next_attempt)) AS m(id, status, next_attempt)
 WHERE msgs_msg.id = m.id::bigint`

func updateMessageStatus(ctx context.Context, db DBorTx, msgs []*Msg, status MsgStatus, nextAttempt *time.Time) error {
	is := make([]any, len(msgs))
	for i, msg := range msgs {
		m := &msg.m
		m.Status = status
		m.NextAttempt = nextAttempt
		is[i] = m
	}

	return BulkQuery(ctx, "updating message status", db, sqlUpdateMsgStatus, is)
}

// PrepareMessagesForRetry prepares messages for retrying by fetching the URN and marking them as QUEUED
func PrepareMessagesForRetry(ctx context.Context, db *sqlx.DB, msgs []*Msg) ([]*MsgOut, error) {
	ids := make([]URNID, 0, len(msgs))
	for _, s := range msgs {
		ids = append(ids, s.ContactURNID())
	}

	cus, err := LoadContactURNs(ctx, db, ids)
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
			Msg: m,
			URN: urnsByID[m.ContactURNID()],
		}
	}

	// mark messages as QUEUED
	if err := MarkMessagesQueued(ctx, db, msgs); err != nil {
		return nil, fmt.Errorf("error updating messages for resending: %w", err)
	}

	return retries, nil
}

const sqlUpdateMsgForResending = `
UPDATE msgs_msg m
   SET channel_id = r.channel_id::int,
       status = 'Q',
       error_count = 0,
       failed_reason = NULL,
       sent_on = NULL,
       modified_on = NOW()
  FROM (VALUES(:id, :channel_id)) AS r(id, channel_id)
 WHERE m.id = r.id::bigint`

const sqlUpdateMsgResendFailed = `
UPDATE msgs_msg m
   SET channel_id = NULL, status = 'F', error_count = 0, failed_reason = 'D', sent_on = NULL, modified_on = NOW()
 WHERE id = ANY($1)`

// PrepareMessagesForResend prepares messages for resending by reselecting a channel and marking them as QUEUED
func PrepareMessagesForResend(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, msgs []*Msg) ([]*MsgOut, error) {
	channels := oa.SessionAssets().Channels()

	// for the bulk db updates
	resends := make([]any, 0, len(msgs))
	refails := make([]MsgID, 0, len(msgs))

	resent := make([]*MsgOut, 0, len(msgs))

	for _, msg := range msgs {
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
			fu, err := flows.ParseRawURN(channels, urn, assets.IgnoreMissing)
			if err != nil {
				return nil, fmt.Errorf("error parsing URN: %w", err)
			}

			if fch := channels.GetForURN(fu, assets.ChannelRoleSend); fch != nil {
				ch = oa.ChannelByUUID(fch.UUID())
			}
		}

		if ch != nil {
			msg.m.ChannelID = ch.ID()
			msg.m.Status = MsgStatusPending
			msg.m.SentOn = nil
			msg.m.ErrorCount = 0
			msg.m.FailedReason = ""

			resends = append(resends, msg.m)
			resent = append(resent, &MsgOut{Msg: msg, URN: cu, IsResend: true})
		} else {
			// if we don't have channel or a URN, fail again
			msg.m.ChannelID = NilChannelID
			msg.m.Status = MsgStatusFailed
			msg.m.SentOn = nil
			msg.m.ErrorCount = 0
			msg.m.FailedReason = MsgFailedNoDestination

			refails = append(refails, MsgID(msg.m.ID))
		}
	}

	// update the messages that can be resent
	err := BulkQuery(ctx, "updating messages for resending", rt.DB, sqlUpdateMsgForResending, resends)
	if err != nil {
		return nil, fmt.Errorf("error updating messages for resending: %w", err)
	}

	// and update the messages that can't be
	_, err = rt.DB.ExecContext(ctx, sqlUpdateMsgResendFailed, pq.Array(refails))
	if err != nil {
		return nil, fmt.Errorf("error updating non-resendable messages: %w", err)
	}

	return resent, nil
}

const sqlFailChannelMessages = `
WITH rows AS (
	SELECT id FROM msgs_msg
	WHERE org_id = $1 AND direction = 'O' AND channel_id = $2 AND status IN ('P', 'Q', 'E') 
	LIMIT 1000
)
UPDATE msgs_msg SET status = 'F', failed_reason = $3, modified_on = NOW() WHERE id IN (SELECT id FROM rows)`

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

// CreateMsgOut creates a new outgoing message to the given contact, resolving the destination etc
func CreateMsgOut(rt *runtime.Runtime, oa *OrgAssets, c *flows.Contact, content *flows.MsgContent, templateID TemplateID, templateVariables []string, locale i18n.Locale, expressionsContext *types.XObject) (*flows.MsgOut, error) {
	// resolve URN + channel for this contact
	urn := urns.NilURN
	var channel *Channel
	var channelRef *assets.ChannelReference
	for _, dest := range c.ResolveDestinations(false) {
		urn = dest.URN.URN()
		channel = oa.ChannelByUUID(dest.Channel.UUID())
		channelRef = dest.Channel.Reference()
		break
	}

	// if there's an expressions context, evaluate text etc
	if expressionsContext != nil {
		ev := goflow.Engine(rt).Evaluator()

		content.Text, _, _ = ev.Template(oa.Env(), expressionsContext, content.Text, nil)
		templateVariables = slices.Clone(templateVariables)

		for i := range content.Attachments {
			evaluated, _, _ := ev.Template(oa.Env(), expressionsContext, string(content.Attachments[i]), nil)
			content.Attachments[i] = utils.Attachment(evaluated)
		}
		for i := range content.QuickReplies {
			content.QuickReplies[i].Text, _, _ = ev.Template(oa.Env(), expressionsContext, content.QuickReplies[i].Text, nil)
		}
		for i := range templateVariables {
			templateVariables[i], _, _ = ev.Template(oa.Env(), expressionsContext, templateVariables[i], nil)
		}
	}

	// if we have a template, try to generate templating
	var templating *flows.MsgTemplating
	if templateID != NilTemplateID && channel != nil {
		template := oa.TemplateByID(templateID)
		if template != nil {
			flowTemplate := flows.NewTemplate(template)
			flowChannel := flows.NewChannel(channel)

			// look for a translation in the contact's locale, or the org's default locale
			locales := make([]i18n.Locale, 0, 2)
			if c.Language() != "" {
				locales = append(locales, c.Locale(oa.Env()))
			}
			locales = append(locales, oa.Env().DefaultLocale())

			trans := flowTemplate.FindTranslation(flowChannel, locales)
			if trans != nil {
				translation := flows.NewTemplateTranslation(trans)
				templating = flows.NewTemplate(template).Templating(translation, templateVariables)

				// override message content to be a preview of template message and override locale to match the template translation
				content = translation.Preview(templating.Variables)
				locale = translation.Locale()
			}
		}
	}

	// is this message sendable?
	unsendableReason := flows.NilUnsendableReason
	if c.Status() != flows.ContactStatusActive {
		unsendableReason = flows.UnsendableReasonContactStatus
	} else if urn == urns.NilURN || channel == nil {
		unsendableReason = flows.UnsendableReasonNoDestination
	} else {
		var err error
		unsendableReason, err = msgCheckSendable(rt, oa.Org(), c, content)
		if err != nil {
			return nil, fmt.Errorf("error checking if message is sendable: %w", err)
		}
	}

	return flows.NewMsgOut(urn, channelRef, content, templating, locale, unsendableReason), nil
}

const sqlUpdateMsgDeletedBySender = `
UPDATE msgs_msg
   SET visibility = 'X', text = '', attachments = '{}'
 WHERE id = $1 AND org_id = $2 AND direction = 'I'`

func UpdateMessageDeletedBySender(ctx context.Context, db *sql.DB, orgID OrgID, msgID MsgID) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	res, err := tx.ExecContext(ctx, sqlUpdateMsgDeletedBySender, msgID, orgID)
	if err != nil {
		return fmt.Errorf("error updating message visibility: %w", err)
	}

	// if there was such a message, remove its labels too
	if rows, _ := res.RowsAffected(); rows == 1 {
		_, err = tx.ExecContext(ctx, `DELETE FROM msgs_msg_labels WHERE msg_id = $1`, msgID)
		if err != nil {
			return fmt.Errorf("error removing message labels: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
}

func msgCheckSendable(rt *runtime.Runtime, org *Org, contact *flows.Contact, content *flows.MsgContent) (flows.UnsendableReason, error) {
	if org.Suspended() {
		return UnsendableReasonOrgStatus, nil
	}

	// does this look like a message loop?
	repetitions, err := GetMsgRepetitions(rt.VK, contact, content)
	if err != nil {
		return flows.NilUnsendableReason, fmt.Errorf("error looking up msg repetitions: %w", err)
	}
	if repetitions > msgRepetitionLimit {
		slog.Warn("too many repetitions, failing message", "contact_id", contact.ID(), "text", content.Text, "repetitions", repetitions)

		return UnsendableReasonLooping, nil
	}

	return flows.NilUnsendableReason, nil
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
