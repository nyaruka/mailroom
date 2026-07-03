package models_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/centrifugo"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vinovest/sqlx"
)

func TestImportNotifications(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	editorSocket := fmt.Sprintf("notifications:%s:%s", testdb.Org1.UUID, testdb.Editor.UUID)

	// mark the creator's notifications socket subscribed so the finished notification is published to it
	vc := rt.VK.Get()
	defer vc.Close()
	_, err = vc.Do("SET", centrifugo.SubscriptionKey(editorSocket), "1")
	require.NoError(t, err)

	importID := testdb.InsertContactImport(t, rt, testdb.Org1, models.ImportStatusProcessing, testdb.Editor)
	imp, err := models.LoadContactImport(ctx, rt.DB, importID)
	require.NoError(t, err)

	err = imp.SetFinished(ctx, rt.DB, true)
	require.NoError(t, err)

	t0 := time.Now()

	err = models.NotifyImportFinished(ctx, rt, oa, imp)
	require.NoError(t, err)

	assertNotifications(t, ctx, rt.DB, t0, map[*testdb.User][]models.NotificationType{
		testdb.Editor: {models.NotificationTypeImportFinished},
	})

	// the notification was also published to the creator's realtime socket as the same JSON the API would serve
	sent := testsuite.CentrifugoHistory(t, rt, editorSocket)
	require.Len(t, sent, 1)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(sent[0], &decoded))
	assert.Equal(t, "import:finished", decoded["type"])
	assert.Equal(t, false, decoded["is_seen"])
	assert.Equal(t, map[string]any{"type": "contact", "num_records": float64(30)}, decoded["import"])
}

func TestIncidentNotifications(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	t0 := time.Now()

	_, _, err = models.IncidentWebhooksUnhealthy(ctx, rt.DB, rt.VK, oa, nil)
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
