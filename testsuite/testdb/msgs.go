package testdb

import (
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/require"
)

type Msg struct {
	ID   models.MsgID
	UUID events.EventUUID
}

type MsgIn struct {
	Msg
	FlowMsg *core.MsgIn
}

func (m *MsgIn) Label(rt *runtime.Runtime, labels ...*Label) {
	for _, l := range labels {
		rt.DB.MustExec(`INSERT INTO msgs_msg_labels(msg_id, label_id) VALUES($1, $2)`, m.ID, l.ID)
	}
}

type MsgOut struct {
	Msg
	FlowMsg *core.MsgOut
}

type Label struct {
	ID   models.LabelID
	UUID assets.LabelUUID
}

type Template struct {
	ID   models.TemplateID
	UUID assets.TemplateUUID
}

type Broadcast struct {
	ID   models.BroadcastID
	UUID core.BroadcastUUID
}

// InsertIncomingMsg inserts an incoming text message, deriving created_on from the v7 UUID timestamp
func InsertIncomingMsg(t *testing.T, rt *runtime.Runtime, org *Org, uuid events.EventUUID, channel *Channel, contact *Contact, text string, status models.MsgStatus, ticketUUID core.TicketUUID) *MsgIn {
	createdOn, err := uuids.V7Time(uuids.UUID(uuid))
	require.NoError(t, err)

	var id models.MsgID
	err = rt.DB.Get(&id,
		`INSERT INTO msgs_msg(uuid, text, created_on, modified_on, direction, msg_type, status, visibility, folder, msg_count, error_count, next_attempt, contact_id, contact_urn_id, org_id, channel_id, ticket_uuid, is_android)
	  	 VALUES($1, $2, $3, NOW(), 'I', $4, $5, 'V', $6, 1, 0, NOW(), $7, $8, $9, $10, $11, FALSE) RETURNING id`, uuid, text, createdOn, models.MsgTypeText, status,
		models.DeriveMsgFolder(models.DirectionIn, status, models.VisibilityVisible, false), contact.ID, contact.URNID, org.ID, channel.ID, null.String(ticketUUID),
	)
	require.NoError(t, err)

	fm := core.NewMsgIn(contact.URN, assets.NewChannelReference(channel.UUID, ""), text, nil, "", nil)
	return &MsgIn{Msg: Msg{ID: id, UUID: uuid}, FlowMsg: fm}
}

// InsertOutgoingMsg inserts an outgoing text message
func InsertOutgoingMsg(t *testing.T, rt *runtime.Runtime, org *Org, uuid events.EventUUID, channel *Channel, contact *Contact, text string, attachments []utils.Attachment, status models.MsgStatus, highPriority bool) *MsgOut {
	return insertOutgoingMsg(t, rt, org, uuid, channel, contact, text, attachments, i18n.Locale(`eng-US`), models.MsgTypeText, status, highPriority, 0, nil, nil)
}

// InsertOutgoingMsgCreatedOn inserts an outgoing text message created at the given time. Messages have a trigger
// which rejects any change to created_on, so tests that need an old message have to insert it as one.
func InsertOutgoingMsgCreatedOn(t *testing.T, rt *runtime.Runtime, org *Org, uuid events.EventUUID, channel *Channel, contact *Contact, text string, status models.MsgStatus, createdOn time.Time) *MsgOut {
	return insertOutgoingMsg(t, rt, org, uuid, channel, contact, text, nil, i18n.Locale(`eng-US`), models.MsgTypeText, status, false, 0, nil, &createdOn)
}

// InsertErroredOutgoingMsg inserts an ERRORED(E) outgoing text message
func InsertErroredOutgoingMsg(t *testing.T, rt *runtime.Runtime, org *Org, channel *Channel, contact *Contact, text string, errorCount int, nextAttempt time.Time, highPriority bool) *MsgOut {
	return insertOutgoingMsg(t, rt, org, events.NewEventUUID(), channel, contact, text, nil, i18n.NilLocale, models.MsgTypeText, models.MsgStatusErrored, highPriority, errorCount, &nextAttempt, nil)
}

func insertOutgoingMsg(t *testing.T, rt *runtime.Runtime, org *Org, uuid events.EventUUID, channel *Channel, contact *Contact, text string, attachments []utils.Attachment, locale i18n.Locale, typ models.MsgType, status models.MsgStatus, highPriority bool, errorCount int, nextAttempt *time.Time, createdOn *time.Time) *MsgOut {
	var channelRef *assets.ChannelReference
	var channelID models.ChannelID
	var isAndroid bool
	if channel != nil {
		channelRef = assets.NewChannelReference(channel.UUID, "")
		channelID = channel.ID
		isAndroid = channel.Type == models.ChannelTypeAndroid
	}

	var sentOn *time.Time
	if status == models.MsgStatusWired || status == models.MsgStatusSent || status == models.MsgStatusDelivered || status == models.MsgStatusRead {
		t := dates.Now()
		sentOn = &t
	}

	fm := core.NewMsgOut(contact.URN, channelRef, &core.MsgContent{Text: text, Attachments: attachments}, nil, i18n.NilLocale, "")

	var id models.MsgID
	err := rt.DB.Get(&id,
		`INSERT INTO msgs_msg(uuid, text, attachments, locale, created_on, modified_on, direction, msg_type, status, visibility, folder, contact_id, contact_urn_id, org_id, channel_id, sent_on, msg_count, error_count, next_attempt, high_priority, is_android)
	  	 VALUES($1, $2, $3, $4, COALESCE($17, NOW()), NOW(), 'O', $5, $6, 'V', $7, $8, $9, $10, $11, $12, 1, $13, $14, $15, $16) RETURNING id`,
		uuid, text, pq.Array(attachments), locale, typ, status, models.DeriveMsgFolder(models.DirectionOut, status, models.VisibilityVisible, false),
		contact.ID, contact.URNID, org.ID, channelID, sentOn, errorCount, nextAttempt, highPriority, isAndroid, createdOn,
	)
	require.NoError(t, err)

	return &MsgOut{Msg: Msg{ID: id, UUID: uuid}, FlowMsg: fm}
}

func InsertBroadcast(t *testing.T, rt *runtime.Runtime, org *Org, uuid core.BroadcastUUID, baseLanguage i18n.Language, text map[i18n.Language]string, schedID models.ScheduleID, contacts []*Contact, groups []*Group) *Broadcast {
	translations := make(core.BroadcastTranslations)
	for lang, t := range text {
		translations[lang] = &core.MsgContent{Text: t}
	}

	var id models.BroadcastID
	err := rt.DB.Get(&id,
		`INSERT INTO msgs_broadcast(uuid, org_id, base_language, translations, schedule_id, status, created_on, modified_on, created_by_id, modified_by_id, is_active)
		VALUES($1, $2, $3, $4, $5, 'P', NOW(), NOW(), 1, 1, TRUE) RETURNING id`, uuid, org.ID, baseLanguage, models.JSONB[core.BroadcastTranslations]{V: translations}, schedID,
	)
	require.NoError(t, err)

	for _, contact := range contacts {
		rt.DB.MustExec(`INSERT INTO msgs_broadcast_contacts(broadcast_id, contact_id) VALUES($1, $2)`, id, contact.ID)
	}
	for _, group := range groups {
		rt.DB.MustExec(`INSERT INTO msgs_broadcast_groups(broadcast_id, contactgroup_id) VALUES($1, $2)`, id, group.ID)
	}

	return &Broadcast{ID: id, UUID: uuid}
}

// InsertTemplate inserts a template
func InsertTemplate(t *testing.T, rt *runtime.Runtime, org *Org, name string) *Template {
	uuid := assets.TemplateUUID(uuids.NewV4())
	var id models.TemplateID
	err := rt.DB.Get(&id,
		`INSERT INTO templates_template(uuid, org_id, name, created_on, modified_on) 
		VALUES($1, $2, $3, NOW(), NOW()) RETURNING id`, uuid, org.ID, name,
	)
	require.NoError(t, err)
	return &Template{ID: id, UUID: uuid}
}
