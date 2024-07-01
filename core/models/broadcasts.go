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
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

// BroadcastID is our internal type for broadcast ids, which can be null/0
type BroadcastID int

// NilBroadcastID is our constant for a nil broadcast id
const NilBroadcastID = BroadcastID(0)

// TemplateState represents what state are templates are in, either already evaluated or unevaluated
type TemplateState string

const (
	TemplateStateEvaluated   = TemplateState("evaluated")
	TemplateStateUnevaluated = TemplateState("unevaluated")
)

// Broadcast represents a broadcast that needs to be sent
type Broadcast struct {
	ID                BroadcastID                 `json:"broadcast_id,omitempty"`
	OrgID             OrgID                       `json:"org_id"`
	Translations      flows.BroadcastTranslations `json:"translations"`
	TemplateState     TemplateState               `json:"template_state"`
	BaseLanguage      i18n.Language               `json:"base_language"`
	OptInID           OptInID                     `json:"optin_id,omitempty"`
	TemplateID        TemplateID                  `json:"template_id,omitempty"`
	TemplateVariables []string                    `json:"template_variables,omitempty"`
	GroupIDs          []GroupID                   `json:"group_ids,omitempty"`
	ContactIDs        []ContactID                 `json:"contact_ids,omitempty"`
	URNs              []urns.URN                  `json:"urns,omitempty"`
	Query             string                      `json:"query,omitempty"`
	Exclusions        Exclusions                  `json:"exclusions,omitempty"`
	CreatedByID       UserID                      `json:"created_by_id,omitempty"`
	ScheduleID        ScheduleID                  `json:"schedule_id,omitempty"`
	ParentID          BroadcastID                 `json:"parent_id,omitempty"`
}

type dbBroadcast struct {
	ID                BroadcastID                        `db:"id"`
	OrgID             OrgID                              `db:"org_id"`
	Translations      JSONB[flows.BroadcastTranslations] `db:"translations"`
	BaseLanguage      i18n.Language                      `db:"base_language"`
	OptInID           OptInID                            `db:"optin_id"`
	TemplateID        TemplateID                         `db:"template_id"`
	TemplateVariables pq.StringArray                     `db:"template_variables"`
	URNs              pq.StringArray                     `db:"urns"`
	Query             null.String                        `db:"query"`
	Exclusions        Exclusions                         `db:"exclusions"`
	CreatedByID       UserID                             `db:"created_by_id"`
	ScheduleID        ScheduleID                         `db:"schedule_id"`
	ParentID          BroadcastID                        `db:"parent_id"`
}

var ErrNoRecipients = errors.New("can't create broadcast with no recipients")

// NewBroadcast creates a new broadcast with the passed in parameters
func NewBroadcast(orgID OrgID, translations flows.BroadcastTranslations,
	state TemplateState, baseLanguage i18n.Language, optInID OptInID, groupIDs []GroupID, contactIDs []ContactID, urns []urns.URN, query string, exclude Exclusions, createdByID UserID) *Broadcast {

	return &Broadcast{
		OrgID:         orgID,
		Translations:  translations,
		TemplateState: state,
		BaseLanguage:  baseLanguage,
		OptInID:       optInID,
		GroupIDs:      groupIDs,
		ContactIDs:    contactIDs,
		URNs:          urns,
		Query:         query,
		Exclusions:    exclude,
		CreatedByID:   createdByID,
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

	return NewBroadcast(oa.OrgID(), event.Translations, TemplateStateEvaluated, event.BaseLanguage, NilOptInID, groupIDs, contactIDs, event.URNs, event.ContactQuery, NoExclusions, NilUserID), nil
}

func (b *Broadcast) CreateBatch(contactIDs []ContactID, isLast bool) *BroadcastBatch {
	return &BroadcastBatch{
		BroadcastID:   b.ID,
		OrgID:         b.OrgID,
		BaseLanguage:  b.BaseLanguage,
		Translations:  b.Translations,
		TemplateState: b.TemplateState,
		OptInID:       b.OptInID,
		CreatedByID:   b.CreatedByID,
		ContactIDs:    contactIDs,
		IsLast:        isLast,
	}
}

// MarkBroadcastSent marks the given broadcast as sent
func MarkBroadcastSent(ctx context.Context, db DBorTx, id BroadcastID) error {
	_, err := db.ExecContext(ctx, `UPDATE msgs_broadcast SET status = 'S', modified_on = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("error marking broadcast #%d as sent: %w", id, err)
	}
	return nil
}

// MarkBroadcastFailed marks the given broadcast as failed
func MarkBroadcastFailed(ctx context.Context, db DBorTx, id BroadcastID) error {
	_, err := db.ExecContext(ctx, `UPDATE msgs_broadcast SET status = 'S', modified_on = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("error marking broadcast #%d as failed: %w", id, err)
	}
	return nil
}

// InsertBroadcast inserts the given broadcast into the DB
func InsertBroadcast(ctx context.Context, db DBorTx, bcast *Broadcast) error {
	dbb := &dbBroadcast{
		ID:                bcast.ID,
		OrgID:             bcast.OrgID,
		Translations:      JSONB[flows.BroadcastTranslations]{bcast.Translations},
		BaseLanguage:      bcast.BaseLanguage,
		OptInID:           bcast.OptInID,
		TemplateID:        bcast.TemplateID,
		TemplateVariables: StringArray(bcast.TemplateVariables),
		URNs:              StringArray(bcast.URNs),
		Query:             null.String(bcast.Query),
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
	err = BulkQuery(ctx, "inserting broadcast contacts", db, sqlInsertBroadcastContacts, contacts)
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
		Translations:      parent.Translations,
		TemplateState:     parent.TemplateState,
		BaseLanguage:      parent.BaseLanguage,
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
	msgs_broadcast( org_id,  parent_id, created_on, modified_on, status,  translations,  base_language,  template_id,  template_variables,  urns,  query,  exclusions,  optin_id,  schedule_id, is_active)
			VALUES(:org_id, :parent_id, NOW()     , NOW(),       'Q',    :translations, :base_language, :template_id, :template_variables, :urns, :query, :exclusions, :optin_id, :schedule_id,      TRUE)
RETURNING id`

const sqlInsertBroadcastContacts = `INSERT INTO msgs_broadcast_contacts(broadcast_id, contact_id) VALUES(:broadcast_id, :contact_id)`
const sqlInsertBroadcastGroups = `INSERT INTO msgs_broadcast_groups(broadcast_id, contactgroup_id) VALUES(:broadcast_id, :contactgroup_id)`

// BroadcastBatch represents a batch of contacts that need messages sent for
type BroadcastBatch struct {
	BroadcastID       BroadcastID                 `json:"broadcast_id,omitempty"`
	OrgID             OrgID                       `json:"org_id"`
	Translations      flows.BroadcastTranslations `json:"translations"`
	BaseLanguage      i18n.Language               `json:"base_language"`
	TemplateState     TemplateState               `json:"template_state"`
	OptInID           OptInID                     `json:"optin_id,omitempty"`
	TemplateID        TemplateID                  `json:"template_id,omitempty"`
	TemplateVariables []string                    `json:"template_variables,omitempty"`
	ContactIDs        []ContactID                 `json:"contact_ids"`
	CreatedByID       UserID                      `json:"created_by_id"`
	IsLast            bool                        `json:"is_last"`
}

func (b *BroadcastBatch) CreateMessages(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets) ([]*Msg, error) {
	// load all our contacts
	contacts, err := LoadContacts(ctx, rt.DB, oa, b.ContactIDs)
	if err != nil {
		return nil, fmt.Errorf("error loading contacts for broadcast: %w", err)
	}

	// for each contact, build our message
	msgs := make([]*Msg, 0, len(contacts))

	// run through all our contacts to create our messages
	for _, c := range contacts {
		msg, err := b.createMessage(rt, oa, c)
		if err != nil {
			return nil, fmt.Errorf("error creating broadcast message: %w", err)
		}
		if msg != nil {
			msgs = append(msgs, msg)
		}
	}

	// insert them in a single request
	err = InsertMessages(ctx, rt.DB, msgs)
	if err != nil {
		return nil, fmt.Errorf("error inserting broadcast messages: %w", err)
	}

	return msgs, nil
}

// creates an outgoing message for the given contact - can return nil if resultant message has no content and thus is a noop
func (b *BroadcastBatch) createMessage(rt *runtime.Runtime, oa *OrgAssets, c *Contact) (*Msg, error) {
	contact, err := c.FlowContact(oa)
	if err != nil {
		return nil, fmt.Errorf("error creating flow contact for broadcast message: %w", err)
	}

	trans, lang := b.Translations.ForContact(oa.Env(), contact, b.BaseLanguage)
	if trans == nil {
		// in theory shoud never happen because we shouldn't save a broadcast like this
		return nil, errors.New("broadcast has no translation in base language")
	}

	text := trans.Text
	attachments := trans.Attachments
	quickReplies := trans.QuickReplies
	locale := i18n.NewLocale(lang, i18n.NilCountry)

	if b.TemplateState == TemplateStateUnevaluated {
		// build up the minimum viable context for templates
		templateCtx := types.NewXObject(map[string]types.XValue{
			"contact": flows.Context(oa.Env(), contact),
			"fields":  flows.Context(oa.Env(), contact.Fields()),
			"globals": flows.Context(oa.Env(), oa.SessionAssets().Globals()),
			"urns":    flows.ContextFunc(oa.Env(), contact.URNs().MapContext),
		})
		text, _, _ = goflow.Engine(rt).Evaluator().Template(oa.Env(), templateCtx, text, nil)
	}

	// don't create a message if we have no content
	if text == "" && len(attachments) == 0 && len(trans.QuickReplies) == 0 {
		return nil, nil
	}

	// create our outgoing message
	out, ch := NewMsgOut(oa, contact, text, attachments, quickReplies, locale)

	msg, err := NewOutgoingBroadcastMsg(rt, oa.Org(), ch, contact, out, b)
	if err != nil {
		return nil, fmt.Errorf("error creating outgoing message: %w", err)
	}

	return msg, nil
}
