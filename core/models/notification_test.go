package models_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	openedEvent := models.NewTicketOpenedEvent(flows.NewEventUUID(), modelTicket, openedBy.SafeID(), assignee.SafeID(), "")
	err := models.InsertLegacyTicketEvents(ctx, rt.DB, []*models.TicketEvent{openedEvent})
	require.NoError(t, err)

	return modelTicket, openedEvent
}
