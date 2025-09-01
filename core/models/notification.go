package models

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/nyaruka/gocommon/dbutil"
)

// NotificationID is our type for notification ids
type NotificationID int64

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
}

// NotifyImportFinished notifies the user who created an import that it has finished
func NotifyImportFinished(ctx context.Context, db DBorTx, imp *ContactImport) error {
	n := &Notification{
		OrgID:           imp.OrgID,
		Type:            NotificationTypeImportFinished,
		Scope:           fmt.Sprintf("contact:%d", imp.ID),
		UserID:          imp.CreatedByID,
		Medium:          MediumUI,
		EmailStatus:     EmailStatusNone,
		ContactImportID: imp.ID,
	}

	return InsertNotifications(ctx, db, []*Notification{n})
}

// NotifyIncidentStarted notifies administrators that an incident has started
func NotifyIncidentStarted(ctx context.Context, db DBorTx, oa *OrgAssets, incident *Incident) error {
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
		}
	}

	return InsertNotifications(ctx, db, notifications)
}

var ticketAssignableRoles = []UserRole{UserRoleAdministrator, UserRoleEditor, UserRoleAgent}

func GetTicketAssignableUsers(oa *OrgAssets) []*User {
	return usersWithRoles(oa, ticketAssignableRoles)
}

// NotificationsFromTicketEvents logs the opening of new tickets and notifies all assignable users if tickets is not already assigned
func NotificationsFromTicketEvents(ctx context.Context, db DBorTx, oa *OrgAssets, events []*TicketEvent) error {
	notifyTicketsActivity := make(map[UserID]bool)

	for _, evt := range events {
		switch evt.Type {
		case TicketEventTypeAssigned:
			// notify new ticket assignee if they didn't self-assign
			if evt.AssigneeID != NilUserID && evt.AssigneeID != evt.CreatedByID {
				notifyTicketsActivity[evt.AssigneeID] = true
			}
		}
	}

	notifications := make([]*Notification, 0, len(events))

	for userID := range notifyTicketsActivity {
		notifications = append(notifications, NewTicketActivityNotification(oa.OrgID(), userID))
	}

	return InsertNotifications(ctx, db, notifications)
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
INSERT INTO notifications_notification(org_id,  notification_type,  scope,  user_id,  medium, is_seen,  email_status, created_on,  contact_import_id,  incident_id) 
                               VALUES(:org_id, :notification_type, :scope, :user_id, :medium,   FALSE, :email_status,      NOW(), :contact_import_id, :incident_id) 
							   ON CONFLICT DO NOTHING`

func InsertNotifications(ctx context.Context, db DBorTx, notifications []*Notification) error {
	if err := dbutil.BulkQuery(ctx, db, sqlInsertNotification, notifications); err != nil {
		return fmt.Errorf("error inserting notifications: %w", err)
	}
	return nil
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
