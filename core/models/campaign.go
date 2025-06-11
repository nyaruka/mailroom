package models

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

type CampaignID int
type CampaignUUID uuids.UUID

type CampaignEventID int
type CampaignEventUUID uuids.UUID
type CampaignEventType string
type CampaignEventStatus string
type CampaignEventMode string
type CampaignEventUnit string

const (
	// CreatedOnKey is key of created on system field
	CreatedOnKey = "created_on"

	// LastSeenOnKey is key of last seen on system field
	LastSeenOnKey = "last_seen_on"

	// NilDeliveryHour is our constant for not having a set delivery hour
	NilDeliveryHour = -1

	CampaignEventTypeMessage = CampaignEventType("M")
	CampaignEventTypeFlow    = CampaignEventType("F")

	CampaignEventStatusScheduling = CampaignEventStatus("S")
	CampaignEventStatusReady      = CampaignEventStatus("R")

	CampaignEventModeInterrupt = CampaignEventMode("I") // should interrupt other flows
	CampaignEventModeSkip      = CampaignEventMode("S") // should be skipped if the user is in another flow
	CampaignEventModePassive   = CampaignEventMode("P") // flow is a background flow and should run that way

	CampaignEventUnitMinutes = CampaignEventUnit("M")
	CampaignEventUnitHours   = CampaignEventUnit("H")
	CampaignEventUnitDays    = CampaignEventUnit("D")
	CampaignEventUnitWeeks   = CampaignEventUnit("W")
)

// Campaign is our struct for a campaign and all its events
type Campaign struct {
	c struct {
		ID      CampaignID       `json:"id"`
		UUID    CampaignUUID     `json:"uuid"`
		Name    string           `json:"name"`
		GroupID GroupID          `json:"group_id"`
		Events  []*CampaignEvent `json:"events"`
	}
}

func (c *Campaign) ID() CampaignID           { return c.c.ID }
func (c *Campaign) UUID() CampaignUUID       { return c.c.UUID }
func (c *Campaign) Name() string             { return c.c.Name }
func (c *Campaign) GroupID() GroupID         { return c.c.GroupID }
func (c *Campaign) Events() []*CampaignEvent { return c.c.Events }

// CampaignEvent is our struct for an individual campaign event
type CampaignEvent struct {
	ID          CampaignEventID     `json:"id"`
	UUID        CampaignEventUUID   `json:"uuid"`
	EventType   CampaignEventType   `json:"event_type"`
	Status      CampaignEventStatus `json:"status"`
	FireVersion int                 `json:"fire_version"`
	StartMode   CampaignEventMode   `json:"start_mode"`

	RelativeToID  FieldID           `json:"relative_to_id"`
	RelativeToKey string            `json:"relative_to_key"`
	Offset        int               `json:"offset"`
	Unit          CampaignEventUnit `json:"unit"`
	DeliveryHour  int               `json:"delivery_hour"`

	FlowID       FlowID                      `json:"flow_id"`
	Translations flows.BroadcastTranslations `json:"translations"`
	BaseLanguage null.String                 `json:"base_language"`

	campaign *Campaign
}

// QualifiesByGroup returns whether the passed in contact qualifies for this event by group membership
func (e *CampaignEvent) QualifiesByGroup(contact *flows.Contact) bool {
	for _, g := range contact.Groups().All() {
		if g.Asset().(*Group).ID() == e.campaign.c.GroupID {
			return true
		}
	}
	return false
}

// QualifiesByField returns whether the passed in contact qualifies for this event by group membership
func (e *CampaignEvent) QualifiesByField(contact *flows.Contact) bool {
	switch e.RelativeToKey {
	case CreatedOnKey:
		return true
	case LastSeenOnKey:
		return contact.LastSeenOn() != nil
	default:
		value := contact.Fields()[e.RelativeToKey]
		return value != nil
	}
}

// ScheduleForContact calculates the next fire ( if any) for the passed in contact
func (e *CampaignEvent) ScheduleForContact(tz *time.Location, now time.Time, contact *flows.Contact) (*time.Time, error) {
	// we aren't part of the group, move on
	if !e.QualifiesByGroup(contact) {
		return nil, nil
	}

	var start time.Time

	switch e.RelativeToKey {
	case CreatedOnKey:
		start = contact.CreatedOn()
	case LastSeenOnKey:
		value := contact.LastSeenOn()
		if value == nil {
			return nil, nil
		}
		start = *value
	default:
		// everything else is just a normal field
		value := contact.Fields()[e.RelativeToKey]

		// no value? move on
		if value == nil {
			return nil, nil
		}

		// get the typed value
		typed := value.QueryValue()
		t, isTime := typed.(time.Time)

		// nil or not a date? move on
		if !isTime {
			return nil, nil
		}

		start = t
	}

	// calculate our next fire
	scheduled := e.ScheduleForTime(tz, now, start)

	return scheduled, nil
}

// ScheduleForTime calculates the next fire (if any) for the passed in time and timezone
func (e *CampaignEvent) ScheduleForTime(tz *time.Location, now, start time.Time) *time.Time {
	start = start.In(tz) // convert to our timezone

	// round to next minute, floored at 0 s/ns if we aren't already at 0
	scheduled := start
	if start.Second() > 0 || start.Nanosecond() > 0 {
		scheduled = start.Add(time.Second * 60).Truncate(time.Minute)
	}

	// create our offset
	switch e.Unit {
	case CampaignEventUnitMinutes:
		scheduled = scheduled.Add(time.Minute * time.Duration(e.Offset))
	case CampaignEventUnitHours:
		scheduled = scheduled.Add(time.Hour * time.Duration(e.Offset))
	case CampaignEventUnitDays:
		scheduled = scheduled.AddDate(0, 0, e.Offset)
	case CampaignEventUnitWeeks:
		scheduled = scheduled.AddDate(0, 0, e.Offset*7)
	}

	// now set our delivery hour if set
	if e.DeliveryHour != NilDeliveryHour {
		scheduled = time.Date(scheduled.Year(), scheduled.Month(), scheduled.Day(), e.DeliveryHour, 0, 0, 0, tz)
	}

	// if this is in the past, this is a no op
	if scheduled.Before(now) {
		return nil
	}

	return &scheduled
}

func (e *CampaignEvent) Campaign() *Campaign { return e.campaign }

// loadCampaigns loads all the campaigns for the passed in org
func loadCampaigns(ctx context.Context, db *sql.DB, orgID OrgID) ([]*Campaign, error) {
	rows, err := db.QueryContext(ctx, sqlSelectCampaignsByOrg, orgID)
	if err != nil {
		return nil, fmt.Errorf("error querying campaigns for org: %d: %w", orgID, err)
	}
	defer rows.Close()

	campaigns := make([]*Campaign, 0, 2)
	for rows.Next() {
		campaign := &Campaign{}
		err := dbutil.ScanJSON(rows, &campaign.c)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling campaign: %w", err)
		}

		campaigns = append(campaigns, campaign)
	}

	// populate the campaign pointer for each event
	for _, c := range campaigns {
		for _, e := range c.Events() {
			e.campaign = c
		}
	}

	return campaigns, nil
}

const sqlSelectCampaignsByOrg = `
SELECT ROW_TO_JSON(r) FROM (SELECT
    c.id,
    c.uuid,
    c.name,
    c.group_id,
    (SELECT ARRAY_AGG(evs) FROM (
        SELECT e.id, e.uuid, e.event_type, e.status, e.fire_version, e.start_mode, e.relative_to_id, f.key AS relative_to_key, e.offset, e.unit, e.delivery_hour, e.flow_id, e.translations, e.base_language
          FROM campaigns_campaignevent e
          JOIN contacts_contactfield f ON f.id = e.relative_to_id
         WHERE e.campaign_id = c.id AND e.is_active = TRUE AND f.is_active = TRUE
      ORDER BY e.relative_to_id, e.offset
    ) evs) AS events
 FROM campaigns_campaign c
WHERE c.org_id = $1 AND c.is_active = TRUE AND c.is_archived = FALSE
) r;`

// DeleteUnfiredEventsForGroupRemoval deletes any unfired events for all campaigns that are
// based on the passed in group id for all the passed in contacts.
func DeleteUnfiredEventsForGroupRemoval(ctx context.Context, tx DBorTx, oa *OrgAssets, contactIDs []ContactID, groupID GroupID) error {
	fds := make([]*FireDelete, 0, 10)

	for _, c := range oa.CampaignByGroupID(groupID) {
		for _, e := range c.Events() {
			for _, cid := range contactIDs {
				fds = append(fds, &FireDelete{
					ContactID:   cid,
					EventID:     e.ID,
					FireVersion: e.FireVersion,
				})
			}
		}
	}

	return DeleteCampaignContactFires(ctx, tx, fds)
}

// AddCampaignEventsForGroupAddition first removes the passed in contacts from any events that group change may effect, then recreates
// the campaign events they qualify for.
func AddCampaignEventsForGroupAddition(ctx context.Context, tx DBorTx, oa *OrgAssets, contacts []*flows.Contact, groupID GroupID) error {
	cids := make([]ContactID, len(contacts))
	for i, c := range contacts {
		cids[i] = ContactID(c.ID())
	}

	// first remove all unfired events that may be affected by our group change
	err := DeleteUnfiredEventsForGroupRemoval(ctx, tx, oa, cids, groupID)
	if err != nil {
		return fmt.Errorf("error removing unfired campaign events for contacts: %w", err)
	}

	// now calculate which fires need to be added
	fas := make([]*ContactFire, 0, 10)

	tz := oa.Env().Timezone()

	// for each of our contacts
	for _, contact := range contacts {
		// for each campaign that may have changed from this group change
		for _, c := range oa.CampaignByGroupID(groupID) {
			// check each event
			for _, ce := range c.Events() {
				// and if we qualify by field
				if ce.QualifiesByField(contact) {
					// calculate our scheduled fire
					scheduled, err := ce.ScheduleForContact(tz, time.Now(), contact)
					if err != nil {
						return fmt.Errorf("error calculating schedule for event #%d and contact #%d: %w", ce.ID, contact.ID(), err)
					}

					// if we have one, add it to our list for our batch commit
					if scheduled != nil {
						fas = append(fas, NewContactFireForCampaign(oa.OrgID(), ContactID(contact.ID()), ce, *scheduled))
					}
				}
			}
		}
	}

	// add all our new event fires
	return InsertContactFires(ctx, tx, fas)
}

// ScheduleCampaignEvent calculates event fires for new or updated campaign events
func ScheduleCampaignEvent(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, eventID CampaignEventID) error {
	ce := oa.CampaignEventByID(eventID)
	if ce == nil {
		return fmt.Errorf("can't find campaign event with id %d", eventID)
	}

	field := oa.FieldByKey(ce.RelativeToKey)
	if field == nil {
		return fmt.Errorf("can't find field with key %s", ce.RelativeToKey)
	}

	eligible, err := campaignEventEligibleContacts(ctx, rt.DB, ce.campaign.GroupID(), field)
	if err != nil {
		return fmt.Errorf("unable to calculate eligible contacts for event %d: %w", ce.ID, err)
	}

	fas := make([]*ContactFire, 0, len(eligible))
	tz := oa.Env().Timezone()

	for _, el := range eligible {
		start := *el.RelToValue

		// calculate next fire for this contact if any
		if scheduled := ce.ScheduleForTime(tz, time.Now(), start); scheduled != nil {
			fas = append(fas, NewContactFireForCampaign(oa.OrgID(), el.ContactID, ce, *scheduled))
		}
	}

	// add all our new event fires
	if err := InsertContactFires(ctx, rt.DB, fas); err != nil {
		return fmt.Errorf("error inserting new contact fires for event #%d: %w", ce.ID, err)
	}

	ce.Status = CampaignEventStatusReady
	if _, err := rt.DB.ExecContext(ctx, `UPDATE campaigns_campaignevent SET status = 'R' WHERE id = $1`, ce.ID); err != nil {
		return fmt.Errorf("error updating status for event #%d: %w", ce.ID, err)
	}

	return nil
}

type eligibleContact struct {
	ContactID  ContactID  `db:"contact_id"`
	RelToValue *time.Time `db:"rel_to_value"`
}

const sqlEligibleContactsForCreatedOn = `
    SELECT c.id AS contact_id, c.created_on AS rel_to_value
      FROM contacts_contact c
INNER JOIN contacts_contactgroup_contacts gc ON gc.contact_id = c.id
     WHERE gc.contactgroup_id = $1 AND c.is_active = TRUE`

const sqlEligibleContactsForLastSeenOn = `
    SELECT c.id AS contact_id, c.last_seen_on AS rel_to_value
      FROM contacts_contact c
INNER JOIN contacts_contactgroup_contacts gc ON gc.contact_id = c.id
    WHERE gc.contactgroup_id = $1 AND c.is_active = TRUE AND c.last_seen_on IS NOT NULL`

const sqlEligibleContactsForField = `
    SELECT c.id AS contact_id, (c.fields->$2->>'datetime')::timestamptz AS rel_to_value
      FROM contacts_contact c
INNER JOIN contacts_contactgroup_contacts gc ON gc.contact_id = c.id
     WHERE gc.contactgroup_id = $1 AND c.is_active = TRUE AND (c.fields->$2->>'datetime')::timestamptz IS NOT NULL`

func campaignEventEligibleContacts(ctx context.Context, db *sqlx.DB, groupID GroupID, field *Field) ([]*eligibleContact, error) {
	var query string
	var params []any

	switch field.Key() {
	case CreatedOnKey:
		query = sqlEligibleContactsForCreatedOn
		params = []any{groupID}
	case LastSeenOnKey:
		query = sqlEligibleContactsForLastSeenOn
		params = []any{groupID}
	default:
		query = sqlEligibleContactsForField
		params = []any{groupID, field.UUID()}
	}

	rows, err := db.QueryxContext(ctx, query, params...)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("error querying for eligible contacts: %w", err)
	}
	defer rows.Close()

	contacts := make([]*eligibleContact, 0, 100)

	for rows.Next() {
		contact := &eligibleContact{}

		err := rows.StructScan(&contact)
		if err != nil {
			return nil, fmt.Errorf("error scanning eligible contact result: %w", err)
		}

		contacts = append(contacts, contact)
	}

	return contacts, nil
}
