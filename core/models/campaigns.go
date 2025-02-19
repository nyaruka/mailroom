package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/runtime"
)

// CampaignID is our type for campaign ids
type CampaignID int

// CampaignEventID is our type for campaign event ids
type CampaignEventID int

// CampaignUUID is our type for campaign UUIDs
type CampaignUUID uuids.UUID

// CampaignEventUUID is our type for campaign event UUIDs
type CampaignEventUUID uuids.UUID

// OffsetUnit defines what time unit our offset is in
type OffsetUnit string

// StartMode defines how a campaign event should be started
type StartMode string

const (
	// CreatedOnKey is key of created on system field
	CreatedOnKey = "created_on"

	// LastSeenOnKey is key of last seen on system field
	LastSeenOnKey = "last_seen_on"

	// OffsetMinute means our offset is in minutes
	OffsetMinute = OffsetUnit("M")

	// OffsetHour means our offset is in hours
	OffsetHour = OffsetUnit("H")

	// OffsetDay means our offset is in days
	OffsetDay = OffsetUnit("D")

	// OffsetWeek means our offset is in weeks
	OffsetWeek = OffsetUnit("W")

	// NilDeliveryHour is our constant for not having a set delivery hour
	NilDeliveryHour = -1

	// StartModeInterrupt means the flow for this campaign event should interrupt other flows
	StartModeInterrupt = StartMode("I")

	// StartModeSkip means the flow should be skipped if the user is active in another flow
	StartModeSkip = StartMode("S")

	// StartModePassive means the flow should be started without interrupting the user in other flows
	StartModePassive = StartMode("P")
)

// Campaign is our struct for a campaign and all its events
type Campaign struct {
	c struct {
		ID        CampaignID       `json:"id"`
		UUID      CampaignUUID     `json:"uuid"`
		Name      string           `json:"name"`
		GroupID   GroupID          `json:"group_id"`
		GroupUUID assets.GroupUUID `json:"group_uuid"`
		GroupName string           `json:"group_name"`
		Events    []*CampaignEvent `json:"events"`
	}
}

// ID return the database id of this campaign
func (c *Campaign) ID() CampaignID { return c.c.ID }

// UUID returns the UUID of this campaign
func (c *Campaign) UUID() CampaignUUID { return c.c.UUID }

// Name returns the name of this campaign
func (c *Campaign) Name() string { return c.c.Name }

// GroupID returns the id of the group this campaign works against
func (c *Campaign) GroupID() GroupID { return c.c.GroupID }

// GroupUUID returns the uuid of the group this campaign works against
func (c *Campaign) GroupUUID() assets.GroupUUID { return c.c.GroupUUID }

// Events returns the list of events for this campaign
func (c *Campaign) Events() []*CampaignEvent { return c.c.Events }

// CampaignEvent is our struct for an individual campaign event
type CampaignEvent struct {
	e struct {
		ID            CampaignEventID   `json:"id"`
		UUID          CampaignEventUUID `json:"uuid"`
		EventType     string            `json:"event_type"`
		StartMode     StartMode         `json:"start_mode"`
		RelativeToID  FieldID           `json:"relative_to_id"`
		RelativeToKey string            `json:"relative_to_key"`
		Offset        int               `json:"offset"`
		Unit          OffsetUnit        `json:"unit"`
		DeliveryHour  int               `json:"delivery_hour"`
		FlowID        FlowID            `json:"flow_id"`
	}

	campaign *Campaign
}

// UnmarshalJSON is our unmarshaller for json data
func (e *CampaignEvent) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &e.e)
}

// QualifiesByGroup returns whether the passed in contact qualifies for this event by group membership
func (e *CampaignEvent) QualifiesByGroup(contact *flows.Contact) bool {
	return contact.Groups().FindByUUID(e.Campaign().GroupUUID()) != nil
}

// QualifiesByField returns whether the passed in contact qualifies for this event by group membership
func (e *CampaignEvent) QualifiesByField(contact *flows.Contact) bool {
	switch e.RelativeToKey() {
	case CreatedOnKey:
		return true
	case LastSeenOnKey:
		return contact.LastSeenOn() != nil
	default:
		value := contact.Fields()[e.RelativeToKey()]
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

	switch e.RelativeToKey() {
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
		value := contact.Fields()[e.RelativeToKey()]

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
	scheduled, err := e.ScheduleForTime(tz, now, start)
	if err != nil {
		return nil, fmt.Errorf("error calculating offset for start: %s and event: %d: %w", start, e.ID(), err)
	}

	return scheduled, nil
}

// ScheduleForTime calculates the next fire (if any) for the passed in time and timezone
func (e *CampaignEvent) ScheduleForTime(tz *time.Location, now time.Time, start time.Time) (*time.Time, error) {
	// convert to our timezone
	start = start.In(tz)

	// round to next minute, floored at 0 s/ns if we aren't already at 0
	scheduled := start
	if start.Second() > 0 || start.Nanosecond() > 0 {
		scheduled = start.Add(time.Second * 60).Truncate(time.Minute)
	}

	// create our offset
	switch e.Unit() {
	case OffsetMinute:
		scheduled = scheduled.Add(time.Minute * time.Duration(e.Offset()))
	case OffsetHour:
		scheduled = scheduled.Add(time.Hour * time.Duration(e.Offset()))
	case OffsetDay:
		scheduled = scheduled.AddDate(0, 0, e.Offset())
	case OffsetWeek:
		scheduled = scheduled.AddDate(0, 0, e.Offset()*7)
	default:
		return nil, fmt.Errorf("unknown offset unit: %s", e.Unit())
	}

	// now set our delivery hour if set
	if e.DeliveryHour() != NilDeliveryHour {
		scheduled = time.Date(scheduled.Year(), scheduled.Month(), scheduled.Day(), e.DeliveryHour(), 0, 0, 0, tz)
	}

	// if this is in the past, this is a no op
	if scheduled.Before(now) {
		return nil, nil
	}

	return &scheduled, nil
}

// ID returns the database id for this campaign event
func (e *CampaignEvent) ID() CampaignEventID { return e.e.ID }

// UUID returns the UUID of this campaign event
func (e *CampaignEvent) UUID() CampaignEventUUID { return e.e.UUID }

// RelativeToID returns the ID of the field this event is relative to
func (e *CampaignEvent) RelativeToID() FieldID { return e.e.RelativeToID }

// RelativeToKey returns the key of the field this event is relative to
func (e *CampaignEvent) RelativeToKey() string { return e.e.RelativeToKey }

// Offset returns the offset for thi campaign event
func (e *CampaignEvent) Offset() int { return e.e.Offset }

// Unit returns the unit for this campaign event
func (e *CampaignEvent) Unit() OffsetUnit { return e.e.Unit }

// DeliveryHour returns the hour this event should send at, if any
func (e *CampaignEvent) DeliveryHour() int { return e.e.DeliveryHour }

// Campaign returns the campaign this event is part of
func (e *CampaignEvent) Campaign() *Campaign { return e.campaign }

// StartMode returns the start mode for this campaign event
func (e *CampaignEvent) StartMode() StartMode { return e.e.StartMode }

func (e *CampaignEvent) FlowID() FlowID { return e.e.FlowID }

// loadCampaigns loads all the campaigns for the passed in org
func loadCampaigns(ctx context.Context, db *sql.DB, orgID OrgID) ([]*Campaign, error) {
	rows, err := db.QueryContext(ctx, selectCampaignsSQL, orgID)
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

const selectCampaignsSQL = `
SELECT ROW_TO_JSON(r) FROM (SELECT
	c.id as id,
	c.uuid as uuid,
	c.name as name,
	cc.name as group_name,
	cc.uuid as group_uuid,
	c.group_id,
	(SELECT ARRAY_AGG(evs) FROM (
		SELECT
			e.id as id,
            e.uuid as uuid,
			e.event_type as event_type,
			e.start_mode as start_mode,
			e.relative_to_id as relative_to_id,
			f.key as relative_to_key,
            e.offset as offset,
			e.unit as unit,
			e.delivery_hour as delivery_hour,
			e.flow_id as flow_id
		FROM 
			campaigns_campaignevent e
			JOIN contacts_contactfield f on e.relative_to_id = f.id
		WHERE 
			e.campaign_id = c.id AND
			e.is_active = TRUE AND
			f.is_active = TRUE
		ORDER BY
			e.relative_to_id,
			e.offset
    ) evs) as events
FROM 
	campaigns_campaign c
	JOIN contacts_contactgroup cc on c.group_id = cc.id
WHERE 
	c.org_id = $1 AND
	c.is_active = TRUE AND
	c.is_archived = FALSE
) r;
`

// DeleteUnfiredEventsForGroupRemoval deletes any unfired events for all campaigns that are
// based on the passed in group id for all the passed in contacts.
func DeleteUnfiredEventsForGroupRemoval(ctx context.Context, tx DBorTx, oa *OrgAssets, contactIDs []ContactID, groupID GroupID) error {
	fds := make([]*FireDelete, 0, 10)

	for _, c := range oa.CampaignByGroupID(groupID) {
		for _, e := range c.Events() {
			for _, cid := range contactIDs {
				fds = append(fds, &FireDelete{
					EventID:   e.ID(),
					ContactID: cid,
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
			for _, e := range c.Events() {
				// and if we qualify by field
				if e.QualifiesByField(contact) {
					// calculate our scheduled fire
					scheduled, err := e.ScheduleForContact(tz, time.Now(), contact)
					if err != nil {
						return fmt.Errorf("error calculating schedule for event: %d and contact: %d: %w", e.ID(), c.ID(), err)
					}

					// if we have one, add it to our list for our batch commit
					if scheduled != nil {
						fas = append(fas, NewContactFireForCampaign(oa.OrgID(), ContactID(contact.ID()), e.ID(), *scheduled))
					}
				}
			}
		}
	}

	// add all our new event fires
	return InsertContactFires(ctx, tx, fas)
}

// ScheduleCampaignEvent calculates event fires for a new campaign event
func ScheduleCampaignEvent(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, eventID CampaignEventID) error {
	event := oa.CampaignEventByID(eventID)
	if event == nil {
		return fmt.Errorf("can't find campaign event with id %d", eventID)
	}

	field := oa.FieldByKey(event.RelativeToKey())
	if field == nil {
		return fmt.Errorf("can't find field with key %s", event.RelativeToKey())
	}

	eligible, err := campaignEventEligibleContacts(ctx, rt.DB, event.campaign.GroupID(), field)
	if err != nil {
		return fmt.Errorf("unable to calculate eligible contacts for event %d: %w", eventID, err)
	}

	fas := make([]*ContactFire, 0, len(eligible))
	tz := oa.Env().Timezone()

	for _, el := range eligible {
		start := *el.RelToValue

		// calculate next fire for this contact
		scheduled, err := event.ScheduleForTime(tz, time.Now(), start)
		if err != nil {
			return fmt.Errorf("error calculating offset for start: %s and event: %d: %w", start, eventID, err)
		}

		if scheduled != nil {
			fas = append(fas, NewContactFireForCampaign(oa.OrgID(), el.ContactID, eventID, *scheduled))
		}
	}

	// add all our new event fires
	return InsertContactFires(ctx, rt.DB, fas)
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
