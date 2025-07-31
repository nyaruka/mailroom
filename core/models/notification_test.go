package models_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTicketNotifications(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	t0 := time.Now()

	// open unassigned tickets by a flow (i.e. no user)
	ticket1, openedEvent1 := openTicket(t, ctx, rt, nil, nil)
	ticket2, openedEvent2 := openTicket(t, ctx, rt, nil, nil)
	err = models.NotificationsFromTicketEvents(ctx, rt.DB, oa, map[*models.Ticket]*models.TicketEvent{ticket1: openedEvent1, ticket2: openedEvent2})
	require.NoError(t, err)

	// check that all assignable users are notified once
	assertNotifications(t, ctx, rt.DB, t0, map[*testdb.User][]models.NotificationType{
		testdb.Admin:  {models.NotificationTypeTicketsOpened},
		testdb.Editor: {models.NotificationTypeTicketsOpened},
		testdb.Agent:  {models.NotificationTypeTicketsOpened},
	})

	t1 := time.Now()

	// another ticket opened won't create new notifications
	ticket3, openedEvent3 := openTicket(t, ctx, rt, nil, nil)
	err = models.NotificationsFromTicketEvents(ctx, rt.DB, oa, map[*models.Ticket]*models.TicketEvent{ticket3: openedEvent3})
	require.NoError(t, err)

	assertNotifications(t, ctx, rt.DB, t1, map[*testdb.User][]models.NotificationType{})

	// mark all notifications as seen
	rt.DB.MustExec(`UPDATE notifications_notification SET is_seen = TRUE`)

	// open an unassigned ticket by a user
	ticket4, openedEvent4 := openTicket(t, ctx, rt, testdb.Editor, nil)
	err = models.NotificationsFromTicketEvents(ctx, rt.DB, oa, map[*models.Ticket]*models.TicketEvent{ticket4: openedEvent4})
	require.NoError(t, err)

	// check that all assignable users are notified except the user that opened the ticket
	assertNotifications(t, ctx, rt.DB, t1, map[*testdb.User][]models.NotificationType{
		testdb.Admin: {models.NotificationTypeTicketsOpened},
		testdb.Agent: {models.NotificationTypeTicketsOpened},
	})

	t2 := time.Now()
	rt.DB.MustExec(`UPDATE notifications_notification SET is_seen = TRUE`)

	// open an already assigned ticket
	ticket5, openedEvent5 := openTicket(t, ctx, rt, nil, testdb.Agent)
	err = models.NotificationsFromTicketEvents(ctx, rt.DB, oa, map[*models.Ticket]*models.TicketEvent{ticket5: openedEvent5})
	require.NoError(t, err)

	// check that the assigned user gets a ticket activity notification
	assertNotifications(t, ctx, rt.DB, t2, map[*testdb.User][]models.NotificationType{
		testdb.Agent: {models.NotificationTypeTicketsActivity},
	})

	t3 := time.Now()

	// however if a user opens a ticket which is assigned to themselves, no notification
	ticket6, openedEvent6 := openTicket(t, ctx, rt, testdb.Admin, testdb.Admin)
	err = models.NotificationsFromTicketEvents(ctx, rt.DB, oa, map[*models.Ticket]*models.TicketEvent{ticket6: openedEvent6})
	require.NoError(t, err)

	// check that the assigned user gets a ticket activity notification
	assertNotifications(t, ctx, rt.DB, t3, map[*testdb.User][]models.NotificationType{})

	t4 := time.Now()
	rt.DB.MustExec(`UPDATE notifications_notification SET is_seen = TRUE`)

	// now have a user assign existing tickets to another user
	_, err = models.TicketsAssign(ctx, rt.DB, oa, testdb.Admin.ID, []*models.Ticket{ticket1, ticket2}, testdb.Agent.ID)
	require.NoError(t, err)

	// check that the assigned user gets a ticket activity notification
	assertNotifications(t, ctx, rt.DB, t4, map[*testdb.User][]models.NotificationType{
		testdb.Agent: {models.NotificationTypeTicketsActivity},
	})

	t5 := time.Now()
	rt.DB.MustExec(`UPDATE notifications_notification SET is_seen = TRUE`)

	// and finally a user assigning a ticket to themselves
	_, err = models.TicketsAssign(ctx, rt.DB, oa, testdb.Editor.ID, []*models.Ticket{ticket3}, testdb.Editor.ID)
	require.NoError(t, err)

	// no notifications for self-assignment
	assertNotifications(t, ctx, rt.DB, t5, map[*testdb.User][]models.NotificationType{})
}

func TestImportNotifications(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	importID := testdb.InsertContactImport(rt, testdb.Org1, models.ImportStatusProcessing, testdb.Editor)
	imp, err := models.LoadContactImport(ctx, rt.DB, importID)
	require.NoError(t, err)

	err = imp.SetFinished(ctx, rt.DB, true)
	require.NoError(t, err)

	t0 := time.Now()

	err = models.NotifyImportFinished(ctx, rt.DB, imp)
	require.NoError(t, err)

	assertNotifications(t, ctx, rt.DB, t0, map[*testdb.User][]models.NotificationType{
		testdb.Editor: {models.NotificationTypeImportFinished},
	})
}

func TestIncidentNotifications(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	t0 := time.Now()

	_, err = models.IncidentWebhooksUnhealthy(ctx, rt.DB, rt.VK, oa, nil)
	require.NoError(t, err)

	assertNotifications(t, ctx, rt.DB, t0, map[*testdb.User][]models.NotificationType{
		testdb.Admin: {models.NotificationTypeIncidentStarted},
	})
}

func assertNotifications(t *testing.T, ctx context.Context, db *sqlx.DB, after time.Time, expected map[*testdb.User][]models.NotificationType) {
	// check last log
	var notifications []*models.Notification
	err := db.SelectContext(ctx, &notifications, `SELECT id, org_id, notification_type, scope, user_id, is_seen, created_on FROM notifications_notification WHERE created_on > $1 ORDER BY id`, after)
	require.NoError(t, err)

	expectedByID := map[models.UserID][]models.NotificationType{}
	for user, notificationTypes := range expected {
		expectedByID[user.ID] = notificationTypes
	}

	actual := map[models.UserID][]models.NotificationType{}
	for _, notification := range notifications {
		actual[notification.UserID] = append(actual[notification.UserID], notification.Type)
	}

	assert.Equal(t, expectedByID, actual)
}

func openTicket(t *testing.T, ctx context.Context, rt *runtime.Runtime, openedBy *testdb.User, assignee *testdb.User) (*models.Ticket, *models.TicketEvent) {
	ticket := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.SupportTopic, time.Now(), assignee)
	modelTicket := ticket.Load(rt)

	openedEvent := models.NewTicketOpenedEvent(modelTicket, openedBy.SafeID(), assignee.SafeID(), "")
	err := models.InsertTicketEvents(ctx, rt.DB, []*models.TicketEvent{openedEvent})
	require.NoError(t, err)

	return modelTicket, openedEvent
}
