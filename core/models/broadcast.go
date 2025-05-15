package models

import (
	"context"
	"errors"
	"fmt"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

// BroadcastID is our internal type for broadcast ids, which can be null/0
type BroadcastID int

// NilBroadcastID is our constant for a nil broadcast id
const NilBroadcastID = BroadcastID(0)

// BroadcastStatus is the type for the status of a broadcast
type BroadcastStatus string

// start status constants
const (
	BroadcastStatusPending     = BroadcastStatus("P")
	BroadcastStatusQueued      = BroadcastStatus("Q")
	BroadcastStatusStarted     = BroadcastStatus("S")
	BroadcastStatusCompleted   = BroadcastStatus("C")
	BroadcastStatusFailed      = BroadcastStatus("F")
	BroadcastStatusInterrupted = BroadcastStatus("I")
)

// Broadcast represents a broadcast that needs to be sent
type Broadcast struct {
	ID                BroadcastID                 `json:"broadcast_id,omitempty"` // null for non-persisted tasks used by flow actions
	OrgID             OrgID                       `json:"org_id"`
	Status            BroadcastStatus             `json:"status"`
	Translations      flows.BroadcastTranslations `json:"translations"`
	BaseLanguage      i18n.Language               `json:"base_language"`
	Expressions       bool                        `json:"expressions"`
	OptInID           OptInID                     `json:"optin_id,omitempty"`
	TemplateID        TemplateID                  `json:"template_id,omitempty"`
	TemplateVariables []string                    `json:"template_variables,omitempty"`
	GroupIDs          []GroupID                   `json:"group_ids,omitempty"`
	ContactIDs        []ContactID                 `json:"contact_ids,omitempty"`
	URNs              []urns.URN                  `json:"urns,omitempty"`
	Query             string                      `json:"query,omitempty"`
	NodeUUID          flows.NodeUUID              `json:"node_uuid,omitempty"`
	Exclusions        Exclusions                  `json:"exclusions,omitempty"`
	CreatedByID       UserID                      `json:"created_by_id,omitempty"`
	ScheduleID        ScheduleID                  `json:"schedule_id,omitempty"`
	ParentID          BroadcastID                 `json:"parent_id,omitempty"`
}

type dbBroadcast struct {
	ID                BroadcastID                        `db:"id"`
	OrgID             OrgID                              `db:"org_id"`
	Status            BroadcastStatus                    `db:"status"`
	Translations      JSONB[flows.BroadcastTranslations] `db:"translations"`
	BaseLanguage      i18n.Language                      `db:"base_language"`
	OptInID           OptInID                            `db:"optin_id"`
	TemplateID        TemplateID                         `db:"template_id"`
	TemplateVariables pq.StringArray                     `db:"template_variables"`
	URNs              pq.StringArray                     `db:"urns"`
	Query             null.String                        `db:"query"`
	NodeUUID          null.String                        `db:"node_uuid"`
	Exclusions        Exclusions                         `db:"exclusions"`
	CreatedByID       UserID                             `db:"created_by_id"`
	ScheduleID        ScheduleID                         `db:"schedule_id"`
	ParentID          BroadcastID                        `db:"parent_id"`
}

var ErrNoRecipients = errors.New("can't create broadcast with no recipients")

// NewBroadcast creates a new broadcast with the passed in parameters
func NewBroadcast(orgID OrgID, translations flows.BroadcastTranslations,
	baseLanguage i18n.Language, expressions bool, optInID OptInID, groupIDs []GroupID, contactIDs []ContactID, urns []urns.URN, query string, exclude Exclusions, createdByID UserID) *Broadcast {

	return &Broadcast{
		OrgID:        orgID,
		Status:       BroadcastStatusPending,
		Translations: translations,
		BaseLanguage: baseLanguage,
		Expressions:  expressions,
		OptInID:      optInID,
		GroupIDs:     groupIDs,
		ContactIDs:   contactIDs,
		URNs:         urns,
		Query:        query,
		Exclusions:   exclude,
		CreatedByID:  createdByID,
	}
}

// NewBroadcastFromEvent creates a broadcast object from the passed in broadcast event
func NewBroadcastFromEvent(ctx context.Context, tx DBorTx, oa *OrgAssets, event *events.BroadcastCreatedEvent) (*Broadcast, error) {
	// resolve our contact references
	contactIDs, err := GetContactIDsFromReferences(ctx, tx, oa.OrgID(), event.Contacts)
	if err != nil {
		return nil, fmt.Errorf("error resolving contact references: %w", err)
	}

	// and our groups
	groupIDs := make([]GroupID, 0, len(event.Groups))
	for i := range event.Groups {
		group := oa.GroupByUUID(event.Groups[i].UUID)
		if group != nil {
			groupIDs = append(groupIDs, group.ID())
		}
	}

	return NewBroadcast(oa.OrgID(), event.Translations, event.BaseLanguage, false, NilOptInID, groupIDs, contactIDs, event.URNs, event.ContactQuery, NoExclusions, NilUserID), nil
}

// BroadcastBatch represents a batch of contacts that need messages sent for
type BroadcastBatch struct {
	// for persisted starts broadcast_id is set, for non-persisted broadcasts like flow actions, broadcast is set
	BroadcastID BroadcastID `json:"broadcast_id,omitempty"`
	Broadcast   *Broadcast  `json:"broadcast,omitempty"`

	ContactIDs []ContactID `json:"contact_ids"`
	IsFirst    bool        `json:"is_first"`
	IsLast     bool        `json:"is_last"`
}

func (b *Broadcast) CreateBatch(contactIDs []ContactID, isFirst, isLast bool) *BroadcastBatch {
	bb := &BroadcastBatch{
		ContactIDs: contactIDs,
		IsFirst:    isFirst,
		IsLast:     isLast,
	}

	if b.ID != NilBroadcastID {
		bb.BroadcastID = b.ID
	} else {
		bb.Broadcast = b
	}

	return bb
}

// SetStarted sets the status of this broadcast to QUEUED, if it's not already set to INTERRUPTED
func (b *Broadcast) SetQueued(ctx context.Context, db DBorTx, contactCount int) error {
	if b.Status != BroadcastStatusInterrupted {
		b.Status = BroadcastStatusQueued
	}
	if b.ID != NilBroadcastID {
		_, err := db.ExecContext(ctx, "UPDATE msgs_broadcast SET status = 'Q', contact_count = $2, modified_on = NOW() WHERE id = $1 AND status != 'I'", b.ID, contactCount)
		if err != nil {
			return fmt.Errorf("error setting broadcast #%d as queued: %w", b.ID, err)
		}
	}
	return nil
}

// SetStarted sets the status of this broadcast to STARTED, if it's not already set to INTERRUPTED
func (b *Broadcast) SetStarted(ctx context.Context, db DBorTx) error {
	return b.setStatus(ctx, db, BroadcastStatusStarted)
}

// SetCompleted sets the status of this broadcast to COMPLETED, if it's not already set to INTERRUPTED
func (b *Broadcast) SetCompleted(ctx context.Context, db DBorTx) error {
	return b.setStatus(ctx, db, BroadcastStatusCompleted)
}

// SetFailed sets the status of this broadcast to FAILED, if it's not already set to INTERRUPTED
func (b *Broadcast) SetFailed(ctx context.Context, db DBorTx) error {
	return b.setStatus(ctx, db, BroadcastStatusFailed)
}

func (b *Broadcast) setStatus(ctx context.Context, db DBorTx, status BroadcastStatus) error {
	if b.Status != BroadcastStatusInterrupted {
		b.Status = status
	}
	if b.ID != NilBroadcastID {
		_, err := db.ExecContext(ctx, "UPDATE msgs_broadcast SET status = $2, modified_on = NOW() WHERE id = $1 AND status != 'I'", b.ID, status)
		if err != nil {
			return fmt.Errorf("error updating broadcast #%d with status=%s: %w", b.ID, status, err)
		}
	}
	return nil
}

// InsertBroadcast inserts the given broadcast into the DB
func InsertBroadcast(ctx context.Context, db DBorTx, bcast *Broadcast) error {
	dbb := &dbBroadcast{
		ID:                bcast.ID,
		OrgID:             bcast.OrgID,
		Status:            bcast.Status,
		Translations:      JSONB[flows.BroadcastTranslations]{bcast.Translations},
		BaseLanguage:      bcast.BaseLanguage,
		OptInID:           bcast.OptInID,
		TemplateID:        bcast.TemplateID,
		TemplateVariables: StringArray(bcast.TemplateVariables),
		URNs:              StringArray(bcast.URNs),
		Query:             null.String(bcast.Query),
		NodeUUID:          null.String(string(bcast.NodeUUID)),
		Exclusions:        bcast.Exclusions,
		CreatedByID:       bcast.CreatedByID,
		ScheduleID:        bcast.ScheduleID,
		ParentID:          bcast.ParentID,
	}

	err := BulkQuery(ctx, "inserting broadcast", db, sqlInsertBroadcast, []*dbBroadcast{dbb})
	if err != nil {
		return fmt.Errorf("error inserting broadcast: %w", err)
	}

	bcast.ID = dbb.ID

	// build up all our contact associations
	contacts := make([]*broadcastContact, 0, len(bcast.ContactIDs))
	for _, contactID := range bcast.ContactIDs {
		contacts = append(contacts, &broadcastContact{BroadcastID: bcast.ID, ContactID: contactID})
	}

	// insert our contacts
	err = BulkQueryBatches(ctx, "inserting broadcast contacts", db, sqlInsertBroadcastContacts, 1000, contacts)
	if err != nil {
		return fmt.Errorf("error inserting contacts for broadcast: %w", err)
	}

	// build up all our group associations
	groups := make([]*broadcastGroup, 0, len(bcast.GroupIDs))
	for _, groupID := range bcast.GroupIDs {
		groups = append(groups, &broadcastGroup{BroadcastID: bcast.ID, GroupID: groupID})
	}

	// insert our groups
	err = BulkQuery(ctx, "inserting broadcast groups", db, sqlInsertBroadcastGroups, groups)
	if err != nil {
		return fmt.Errorf("error inserting groups for broadcast: %w", err)
	}

	return nil
}

// InsertChildBroadcast clones the passed in broadcast as a parent, then inserts that broadcast into the DB
func InsertChildBroadcast(ctx context.Context, db DBorTx, parent *Broadcast) (*Broadcast, error) {
	child := &Broadcast{
		OrgID:             parent.OrgID,
		Status:            BroadcastStatusPending,
		Translations:      parent.Translations,
		BaseLanguage:      parent.BaseLanguage,
		Expressions:       parent.Expressions,
		OptInID:           parent.OptInID,
		TemplateID:        parent.TemplateID,
		TemplateVariables: parent.TemplateVariables,
		GroupIDs:          parent.GroupIDs,
		ContactIDs:        parent.ContactIDs,
		URNs:              parent.URNs,
		Query:             parent.Query,
		Exclusions:        parent.Exclusions,
		CreatedByID:       parent.CreatedByID,
		ParentID:          parent.ID,
	}

	return child, InsertBroadcast(ctx, db, child)
}

type broadcastContact struct {
	BroadcastID BroadcastID `db:"broadcast_id"`
	ContactID   ContactID   `db:"contact_id"`
}

type broadcastGroup struct {
	BroadcastID BroadcastID `db:"broadcast_id"`
	GroupID     GroupID     `db:"contactgroup_id"`
}

const sqlInsertBroadcast = `
INSERT INTO
	msgs_broadcast( org_id,  parent_id, created_on, modified_on,  status,  translations,  base_language,  template_id,  template_variables,  urns,  query,  node_uuid,  exclusions,  optin_id,  schedule_id, is_active)
			VALUES(:org_id, :parent_id, NOW()     , NOW(),       :status, :translations, :base_language, :template_id, :template_variables, :urns, :query, :node_uuid, :exclusions, :optin_id, :schedule_id,      TRUE)
RETURNING id`

const sqlInsertBroadcastContacts = `INSERT INTO msgs_broadcast_contacts(broadcast_id, contact_id) VALUES(:broadcast_id, :contact_id)`
const sqlInsertBroadcastGroups = `INSERT INTO msgs_broadcast_groups(broadcast_id, contactgroup_id) VALUES(:broadcast_id, :contactgroup_id)`

const sqlGetBroadcastByID = `
SELECT id, org_id, status, translations, base_language, optin_id, template_id, template_variables, created_by_id
  FROM msgs_broadcast 
 WHERE id = $1`

// GetBroadcastByID gets a broadcast by it's ID - NOTE this does not load all attributes of the broadcast
func GetBroadcastByID(ctx context.Context, db DBorTx, bcastID BroadcastID) (*Broadcast, error) {
	b := &dbBroadcast{}
	if err := db.GetContext(ctx, b, sqlGetBroadcastByID, bcastID); err != nil {
		return nil, fmt.Errorf("error loading broadcast #%d: %w", bcastID, err)
	}
	return &Broadcast{
		ID:                b.ID,
		OrgID:             b.OrgID,
		Status:            b.Status,
		Translations:      b.Translations.V,
		BaseLanguage:      b.BaseLanguage,
		Expressions:       true,
		OptInID:           b.OptInID,
		TemplateID:        b.TemplateID,
		TemplateVariables: b.TemplateVariables,
		CreatedByID:       b.CreatedByID,
	}, nil
}

func (b *Broadcast) CreateMessages(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, batch *BroadcastBatch) ([]*MsgOut, error) {
	// load all our contacts
	contacts, err := LoadContacts(ctx, rt.DB, oa, batch.ContactIDs)
	if err != nil {
		return nil, fmt.Errorf("error loading contacts for broadcast: %w", err)
	}

	// for each contact, build our message
	msgs := make([]*Msg, 0, len(contacts))
	sends := make([]*MsgOut, 0, len(contacts))

	// run through all our contacts to create our messages
	for _, c := range contacts {
		send, err := b.createMessage(rt, oa, c)
		if err != nil {
			return nil, fmt.Errorf("error creating broadcast message: %w", err)
		}
		if send != nil {
			msgs = append(msgs, send.Msg)
			sends = append(sends, send)
		}
	}

	if err := InsertMessages(ctx, rt.DB, msgs); err != nil {
		return nil, fmt.Errorf("error inserting broadcast messages: %w", err)
	}

	return sends, nil
}

// creates an outgoing message for the given contact - can return nil if resultant message has no content and thus is a noop
func (b *Broadcast) createMessage(rt *runtime.Runtime, oa *OrgAssets, c *Contact) (*MsgOut, error) {
	contact, err := c.FlowContact(oa)
	if err != nil {
		return nil, fmt.Errorf("error creating flow contact for broadcast message: %w", err)
	}

	content, locale := b.Translations.ForContact(oa.Env(), contact, b.BaseLanguage)

	var expressionsContext *types.XObject
	if b.Expressions {
		expressionsContext = types.NewXObject(map[string]types.XValue{
			"contact": flows.Context(oa.Env(), contact),
			"fields":  flows.Context(oa.Env(), contact.Fields()),
			"globals": flows.Context(oa.Env(), oa.SessionAssets().Globals()),
			"urns":    flows.ContextFunc(oa.Env(), contact.URNs().MapContext),
		})
	}

	// don't create a message if we have no content
	if content.Empty() {
		return nil, nil
	}

	// create our outgoing message
	out, ch := CreateMsgOut(rt, oa, contact, content, b.TemplateID, b.TemplateVariables, locale, expressionsContext)

	msg, err := NewOutgoingBroadcastMsg(rt, oa.Org(), ch, contact, out, b)
	if err != nil {
		return nil, fmt.Errorf("error creating outgoing message: %w", err)
	}

	return msg, nil
}
