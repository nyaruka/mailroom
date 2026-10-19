package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/null/v3"
	"github.com/vinovest/sqlx"
)

type TicketID int

// NilTicketID is our constant for a nil ticket id
const NilTicketID = TicketID(0)

func (i *TicketID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i TicketID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *TicketID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i TicketID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

type TicketStatus string

const (
	TicketStatusOpen   = TicketStatus("O")
	TicketStatusClosed = TicketStatus("C")
)

var ticketStatusMap = map[TicketStatus]core.TicketStatus{
	TicketStatusOpen:   core.TicketStatusOpen,
	TicketStatusClosed: core.TicketStatusClosed,
}

type Ticket struct {
	ID             TicketID        `db:"id"`
	UUID           core.TicketUUID `db:"uuid"`
	OrgID          OrgID           `db:"org_id"`
	ContactID      ContactID       `db:"contact_id"`
	Status         TicketStatus    `db:"status"`
	TopicID        TopicID         `db:"topic_id"`
	AssigneeID     UserID          `db:"assignee_id"`
	OpenedOn       time.Time       `db:"opened_on"`
	OpenedByID     UserID          `db:"opened_by_id"`
	OpenedInID     FlowID          `db:"opened_in_id"`
	RepliedOn      *time.Time      `db:"replied_on"`
	ModifiedOn     time.Time       `db:"modified_on"`
	ClosedOn       *time.Time      `db:"closed_on"`
	LastActivityOn time.Time       `db:"last_activity_on"`
}

// NewTicket creates a new open ticket
func NewTicket(uuid core.TicketUUID, orgID OrgID, userID UserID, flow *Flow, contactID ContactID, topicID TopicID, assigneeID UserID) *Ticket {
	var openedInID FlowID
	if flow != nil {
		openedInID = flow.ID()
	}

	return &Ticket{
		UUID:       uuid,
		OrgID:      orgID,
		OpenedByID: userID,
		OpenedInID: openedInID,
		ContactID:  contactID,
		Status:     TicketStatusOpen,
		TopicID:    topicID,
		AssigneeID: assigneeID,
	}
}

func (t *Ticket) EngineTicket(oa *OrgAssets) *core.Ticket {
	var topic *core.Topic
	if t.TopicID != NilTopicID {
		dbTopic := oa.TopicByID(t.TopicID)
		if dbTopic != nil {
			topic = oa.SessionAssets().Topics().Get(dbTopic.UUID())
		}
	}

	var assignee *core.User
	if t.AssigneeID != NilUserID {
		user := oa.UserByID(t.AssigneeID)
		if user != nil {
			assignee = oa.SessionAssets().Users().Get(user.UUID())
		}
	}

	return core.NewTicket(t.UUID, ticketStatusMap[t.Status], topic, assignee)
}

const sqlSelectTicketsByUUID = `
SELECT
  id,
  uuid,
  org_id,
  contact_id,
  status,
  topic_id,
  assignee_id,
  opened_on,
  opened_by_id,
  opened_in_id,
  replied_on,
  modified_on,
  closed_on,
  last_activity_on
    FROM tickets_ticket
   WHERE org_id = $1 AND uuid = ANY($2)
ORDER BY opened_on DESC`

// LoadTickets loads all of the tickets with the given ids
func LoadTickets(ctx context.Context, db *sqlx.DB, orgID OrgID, uuids []core.TicketUUID) ([]*Ticket, error) {
	return loadTickets(ctx, db, sqlSelectTicketsByUUID, orgID, pq.Array(uuids))
}

func loadTickets(ctx context.Context, db *sqlx.DB, query string, params ...any) ([]*Ticket, error) {
	rows, err := db.QueryxContext(ctx, query, params...)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("error loading tickets: %w", err)
	}
	defer rows.Close()

	tickets := make([]*Ticket, 0, 2)
	for rows.Next() {
		t := &Ticket{}
		if err := rows.StructScan(t); err != nil {
			return nil, fmt.Errorf("error unmarshalling ticket: %w", err)
		}
		tickets = append(tickets, t)
	}

	return tickets, nil
}

const sqlInsertTicket = `
INSERT INTO 
  tickets_ticket(uuid,  org_id,  contact_id,  status,  topic_id,  assignee_id,  opened_on, opened_by_id,  opened_in_id,  modified_on, last_activity_on)
  VALUES(       :uuid, :org_id, :contact_id, :status, :topic_id, :assignee_id,  NOW(),    :opened_by_id, :opened_in_id,  NOW()      , NOW())
RETURNING id`

// InsertTickets inserts the passed in tickets returning any errors encountered
func InsertTickets(ctx context.Context, tx DBorTx, oa *OrgAssets, tickets []*Ticket) error {
	if len(tickets) == 0 {
		return nil
	}

	dailyCounts := make(map[string]int, len(tickets))

	for _, t := range tickets {
		dailyCounts[fmt.Sprintf("tickets:opened:%d", t.TopicID)]++

		if t.AssigneeID != NilUserID {
			assignee := oa.UserByID(t.AssigneeID)
			if assignee != nil {
				teamID := NilTeamID
				if assignee.Team() != nil {
					teamID = assignee.Team().ID
				}
				dailyCounts[fmt.Sprintf("tickets:assigned:%d:%d", teamID, assignee.ID())]++
			}
		}
	}

	if err := BulkQuery(ctx, "inserted tickets", tx, sqlInsertTicket, tickets); err != nil {
		return fmt.Errorf("error inserting tickets: %w", err)
	}

	if err := InsertDailyCounts(ctx, tx, oa, dates.Now(), dailyCounts); err != nil {
		return fmt.Errorf("error inserting daily counts: %w", err)
	}

	return nil
}

// InactiveTicket is an open ticket that has had no activity since a given time
type InactiveTicket struct {
	ID        TicketID  `db:"id"`
	OrgID     OrgID     `db:"org_id"`
	ContactID ContactID `db:"contact_id"`
}

const sqlSelectInactiveTickets = `
    SELECT t.id, t.org_id, t.contact_id
      FROM tickets_ticket t
INNER JOIN orgs_org o ON o.id = t.org_id
     WHERE t.status = 'O' AND t.last_activity_on < $1 AND o.is_active
  ORDER BY t.last_activity_on
     LIMIT $2`

// GetInactiveTickets gets up to limit open tickets across all orgs that have had no activity since the given time,
// oldest activity first.
func GetInactiveTickets(ctx context.Context, db DBorTx, olderThan time.Time, limit int) ([]*InactiveTicket, error) {
	tickets := make([]*InactiveTicket, 0, limit)
	if err := db.SelectContext(ctx, &tickets, sqlSelectInactiveTickets, olderThan, limit); err != nil {
		return nil, fmt.Errorf("error selecting inactive tickets: %w", err)
	}
	return tickets, nil
}

// UpdateTicketLastActivity updates the last_activity_on of the given tickets to be now
func UpdateTicketLastActivity(ctx context.Context, db DBorTx, tickets []*Ticket) error {
	now := dates.Now()
	ids := make([]TicketID, len(tickets))
	for i, t := range tickets {
		t.LastActivityOn = now
		ids[i] = t.ID
	}

	_, err := db.ExecContext(ctx, `UPDATE tickets_ticket SET last_activity_on = $2 WHERE id = ANY($1)`, pq.Array(ids), now)
	return err
}

const sqlUpdateTicket = `
UPDATE tickets_ticket t
   SET status = r.status,
       assignee_id = r.assignee_id,
       topic_id = r.topic_id,
       last_activity_on = GREATEST(t.last_activity_on, r.last_activity_on),
	   closed_on = r.closed_on,
       modified_on = NOW()
  FROM (VALUES(:id::int, :status, :assignee_id::int, :topic_id::int, :last_activity_on::timestamptz, :closed_on::timestamptz)) AS r(id, status, assignee_id, topic_id, last_activity_on, closed_on)
 WHERE t.id = r.id`

// UpdateTickets updates the passed in tickets in the database
func UpdateTickets(ctx context.Context, tx DBorTx, tickets []*Ticket) error {
	if err := BulkQuery(ctx, "update tickets", tx, sqlUpdateTicket, tickets); err != nil {
		return fmt.Errorf("error updating tickets: %w", err)
	}
	return nil
}

const sqlUpdateTicketRepliedOn = `
   UPDATE tickets_ticket t1
      SET last_activity_on = $2, replied_on = LEAST(t1.replied_on, $2)
	 FROM tickets_ticket t2
    WHERE t1.id = t2.id AND t1.id = $1
RETURNING CASE WHEN t2.replied_on IS NULL THEN t1.opened_on ELSE NULL END`

// RecordTicketReply records a reply by a user to the given ticket, updating last_activity_on and, if this is the first
// reply, replied_on. Also records the reply count and, for a first reply, the response time.
func RecordTicketReply(ctx context.Context, db DBorTx, oa *OrgAssets, ticket *Ticket, userID UserID, when time.Time) error {
	var openedOn *time.Time
	if err := db.GetContext(ctx, &openedOn, sqlUpdateTicketRepliedOn, ticket.ID, when); err != nil {
		return fmt.Errorf("error updating ticket replied_on: %w", err)
	}

	ticket.LastActivityOn = when
	if ticket.RepliedOn == nil || when.Before(*ticket.RepliedOn) {
		ticket.RepliedOn = &when
	}

	teamID := NilTeamID
	if userID != NilUserID {
		user := oa.UserByID(userID)
		if user != nil {
			if user.Team() != nil {
				teamID = user.Team().ID
			}
		}
	}

	// record reply count that encodes team + user
	dailyCounts := map[string]int{fmt.Sprintf("msgs:ticketreplies:%d:%d", teamID, userID): 1}

	if err := InsertDailyCounts(ctx, db, oa, when, dailyCounts); err != nil {
		return fmt.Errorf("error inserting daily counts: %w", err)
	}

	// if this is the first reply to the ticket then record the ticket response time
	if openedOn != nil {
		respSeconds := int(when.Sub(*openedOn) / time.Second)
		timingCounts := map[string]int{"ticketresptime:total": respSeconds, "ticketresptime:count": 1}

		if err := InsertDailyCounts(ctx, db, oa, *openedOn, timingCounts); err != nil {
			return fmt.Errorf("error inserting daily counts: %w", err)
		}
	}

	return nil
}
