package testsuite

import (
	"context"
	"slices"
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func StartSessions(t *testing.T, rt *runtime.Runtime, oa *models.OrgAssets, contacts []*testdb.Contact, trigger any) []*runner.Scene {
	ctx := context.Background()

	var triggers []flows.Trigger
	switch typed := trigger.(type) {
	case []flows.Trigger:
		triggers = typed
	case flows.Trigger:
		triggers = slices.Repeat([]flows.Trigger{typed}, len(contacts))
	default:
		panic("invalid trigger type")
	}

	scenes := make([]*runner.Scene, len(contacts))
	for i, contact := range contacts {
		mc, fc, _ := contact.Load(rt, oa)
		scenes[i] = runner.NewScene(mc, fc)

		err := scenes[i].StartSession(ctx, rt, oa, triggers[i], true)
		require.NoError(t, err)
	}

	err := runner.BulkCommit(context.Background(), rt, oa, scenes)
	require.NoError(t, err)
	return scenes
}

func ResumeSession(t *testing.T, rt *runtime.Runtime, oa *models.OrgAssets, contact *testdb.Contact, resume any) *runner.Scene {
	ctx := context.Background()

	mc, fc, _ := contact.Load(rt, oa)

	require.NotEqual(t, flows.SessionUUID(""), mc.CurrentSessionUUID(), "contact must have a waiting session")

	modelSession, err := models.GetWaitingSessionForContact(ctx, rt, oa, fc, mc.CurrentSessionUUID())
	require.NoError(t, err)

	scene := runner.NewScene(mc, fc)

	var r flows.Resume
	switch typed := resume.(type) {
	case flows.Resume:
		r = typed
	case string:
		msg := flows.NewMsgIn(contact.URN, nil, typed, nil, "")
		r = resumes.NewMsg(events.NewMsgReceived(msg))
	default:
		panic("invalid resume type")
	}

	err = scene.ResumeSession(ctx, rt, oa, modelSession, r)
	require.NoError(t, err)
	err = scene.Commit(ctx, rt, oa)
	require.NoError(t, err)
	return scene
}
