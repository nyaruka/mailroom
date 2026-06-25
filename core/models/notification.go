package models

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// NotificationID is our type for notification ids
type NotificationID int64

// NilNotificationID is the zero value for notification ids
const NilNotificationID = NotificationID(0)

type NotificationType string

const (
	NotificationTypeExportFinished  NotificationType = "export:finished"
	NotificationTypeImportFinished  NotificationType = "import:finished"
	NotificationTypeIncidentStarted NotificationType = "incident:started"
	NotificationTypeTicketsOpened   NotificationType = "tickets:opened"
	NotificationTypeTicketsActivity NotificationType = "tickets:activity"
)

type EmailStatus string

const (
	EmailStatusPending EmailStatus = "P"
	EmailStatusSent    EmailStatus = "S"
	EmailStatusNone    EmailStatus = "N"
)

const (
	MediumUI    = "U"
	MediumEmail = "E"
)

type Notification struct {
	ID          NotificationID   `db:"id"`
	OrgID       OrgID            `db:"org_id"`
	Type        NotificationType `db:"notification_type"`
	Scope       string           `db:"scope"`
	UserID      UserID           `db:"user_id"`
	Medium      string           `db:"medium"`
	IsSeen      bool             `db:"is_seen"`
	EmailStatus EmailStatus      `db:"email_status"`
	CreatedOn   time.Time        `db:"created_on"`

	ContactImportID ContactImportID `db:"contact_import_id"`
	IncidentID      IncidentID      `db:"incident_id"`

	// transient context used to render the realtime socket payload, never persisted
	contactImport *ContactImport
	incident      *Incident
}

// NotifyImportFinished notifies the user who created an import that it has finished, and publishes that notification
// to the user's realtime socket. The import has already been committed (this runs outside any transaction) so the
// notification is published as soon as it's created.
func NotifyImportFinished(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, imp *ContactImport) error {
	n := &Notification{
		OrgID:           imp.OrgID,
		Type:            NotificationTypeImportFinished,
		Scope:           fmt.Sprintf("contact:%d", imp.ID),
		UserID:          imp.CreatedByID,
		Medium:          MediumUI,
		EmailStatus:     EmailStatusNone,
		ContactImportID: imp.ID,
		contactImport:   imp,
	}

	inserted, err := InsertNotifications(ctx, rt.DB, []*Notification{n})
	if err != nil {
		return err
	}

	// realtime delivery is best-effort - a publish failure shouldn't fail the import when the notification is persisted
	if err := PublishNotifications(ctx, rt, oa, inserted); err != nil {
		slog.Error("error publishing import finished notification", "error", err, "import_id", imp.ID)
	}

	return nil
}

// NotifyIncidentStarted notifies administrators that an incident has started, returning the notifications that were
// actually created so the caller can publish them once its transaction has committed.
func NotifyIncidentStarted(ctx context.Context, db DBorTx, oa *OrgAssets, incident *Incident) ([]*Notification, error) {
	admins := usersWithRoles(oa, []UserRole{UserRoleAdministrator})
	notifications := make([]*Notification, len(admins))

	for i, admin := range admins {
		notifications[i] = &Notification{
			OrgID:       incident.OrgID,
			Type:        NotificationTypeIncidentStarted,
			Scope:       strconv.Itoa(int(incident.ID)),
			UserID:      admin.ID(),
			Medium:      MediumUI,
			EmailStatus: EmailStatusNone,
			IncidentID:  incident.ID,
			incident:    incident,
		}
	}

	return InsertNotifications(ctx, db, notifications)
}

var ticketAssignableRoles = []UserRole{UserRoleAdministrator, UserRoleEditor, UserRoleAgent}

// GetTicketAssignableUsers returns all users that can be assigned tickets
// TODO make this part of org assets?
func GetTicketAssignableUsers(oa *OrgAssets) []*User {
	return usersWithRoles(oa, ticketAssignableRoles)
}

func NewTicketsOpenedNotification(orgID OrgID, userID UserID) *Notification {
	return &Notification{
		OrgID:       orgID,
		Type:        NotificationTypeTicketsOpened,
		Scope:       "",
		UserID:      userID,
		Medium:      MediumUI,
		EmailStatus: EmailStatusNone,
	}
}

func NewTicketActivityNotification(orgID OrgID, userID UserID) *Notification {
	return &Notification{
		OrgID:       orgID,
		Type:        NotificationTypeTicketsActivity,
		Scope:       "",
		UserID:      userID,
		Medium:      MediumUI,
		EmailStatus: EmailStatusNone,
	}
}

const sqlInsertNotification = `
INSERT INTO notifications_notification(org_id,  notification_type,  scope,  user_id,  medium, is_seen,  email_status,  created_on,  contact_import_id,  incident_id)
                               VALUES(:org_id, :notification_type, :scope, :user_id, :medium,   FALSE, :email_status, :created_on, :contact_import_id, :incident_id)
                          ON CONFLICT DO NOTHING
                            RETURNING id, org_id, user_id, notification_type, scope`

// InsertNotifications inserts the given notifications and returns those that were actually created. A notification
// that would duplicate an existing unseen one (same org, type, scope and user) is dropped by the ON CONFLICT and not
// returned. Each returned notification has its ID populated; all are stamped with the same CreatedOn.
func InsertNotifications(ctx context.Context, db DBorTx, notifications []*Notification) ([]*Notification, error) {
	if len(notifications) == 0 {
		return nil, nil
	}

	now := dates.Now()
	for _, n := range notifications {
		if n.CreatedOn.IsZero() {
			n.CreatedOn = now
		}
	}

	// we can't use the bulk query helper here: ON CONFLICT DO NOTHING means fewer rows come back than we send, so we
	// build the bulk insert ourselves and match each returned row to its notification by its unique key
	sql, args, err := dbutil.BulkSQL(db, sqlInsertNotification, notifications)
	if err != nil {
		return nil, fmt.Errorf("error building notifications insert: %w", err)
	}

	rows, err := db.QueryxContext(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("error inserting notifications: %w", err)
	}
	defer rows.Close()

	byKey := make(map[string]*Notification, len(notifications))
	for _, n := range notifications {
		byKey[notificationKey(n.OrgID, n.UserID, n.Type, n.Scope)] = n
	}

	inserted := make([]*Notification, 0, len(notifications))
	for rows.Next() {
		r := struct {
			ID    NotificationID   `db:"id"`
			Org   OrgID            `db:"org_id"`
			User  UserID           `db:"user_id"`
			Type  NotificationType `db:"notification_type"`
			Scope string           `db:"scope"`
		}{}
		if err := rows.StructScan(&r); err != nil {
			return nil, fmt.Errorf("error scanning inserted notification: %w", err)
		}
		if n := byKey[notificationKey(r.Org, r.User, r.Type, r.Scope)]; n != nil {
			n.ID = r.ID
			inserted = append(inserted, n)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating inserted notifications: %w", err)
	}

	return inserted, nil
}

// notificationKey identifies a notification within an insert batch by what an unseen notification is unique on (the DB
// index is on org, type, scope and user), so a returned row can be matched back to the notification it was inserted
// from even if a batch ever spans more than one org.
func notificationKey(orgID OrgID, userID UserID, t NotificationType, scope string) string {
	return fmt.Sprintf("%d|%d|%s|%s", orgID, userID, t, scope)
}

// notificationPayload is the JSON shape published to a user's realtime notifications socket. It mirrors the
// representation the web API serves (see temba/notifications), so a client gets the same notification whether it
// fetches the list or receives one live.
type notificationPayload struct {
	Type      NotificationType `json:"type"`
	CreatedOn time.Time        `json:"created_on"`
	URL       string           `json:"url"`
	IsSeen    bool             `json:"is_seen"`

	// at most one of these is set, depending on the notification type
	Import   *importPayload   `json:"import,omitempty"`
	Incident *incidentPayload `json:"incident,omitempty"`
}

type importPayload struct {
	Type       string `json:"type"`
	NumRecords int    `json:"num_records"`
}

type incidentPayload struct {
	Type      IncidentType `json:"type"`
	StartedOn time.Time    `json:"started_on"`
	EndedOn   *time.Time   `json:"ended_on"`
}

// marshalForSocket renders the notification as the JSON published to its user's realtime socket.
func (n *Notification) marshalForSocket() ([]byte, error) {
	p := &notificationPayload{
		Type:      n.Type,
		CreatedOn: n.CreatedOn,
		URL:       fmt.Sprintf("/notification/read/%d/", n.ID),
		IsSeen:    n.IsSeen,
	}

	switch n.Type {
	case NotificationTypeImportFinished:
		if n.contactImport != nil {
			p.Import = &importPayload{Type: "contact", NumRecords: n.contactImport.NumRecords}
		}
	case NotificationTypeIncidentStarted:
		if n.incident != nil {
			p.Incident = &incidentPayload{Type: n.incident.Type, StartedOn: n.incident.StartedOn, EndedOn: n.incident.EndedOn}
		}
	}

	return json.Marshal(p)
}

func usersWithRoles(oa *OrgAssets, roles []UserRole) []*User {
	users := make([]*User, 0, 5)
	for _, u := range oa.users {
		user := u.(*User)
		if hasAnyRole(user, roles) {
			users = append(users, user)
		}
	}
	return users
}

func hasAnyRole(user *User, roles []UserRole) bool {
	return slices.Contains(roles, user.Role())
}
