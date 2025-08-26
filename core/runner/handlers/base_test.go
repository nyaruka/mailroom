package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/definition"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/routers"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ContactActionMap map[*testdb.Contact][]flows.Action
type ContactMsgMap map[*testdb.Contact]*testdb.MsgIn
type ContactModifierMap map[*testdb.Contact][]flows.Modifier

type TestCase struct {
	FlowType        flows.FlowType
	Actions         ContactActionMap
	Msgs            ContactMsgMap
	Modifiers       ContactModifierMap
	ModifierUser    *testdb.User
	Assertions      []Assertion
	SQLAssertions   []SQLAssertion
	PersistedEvents map[string]int
}

type Assertion func(t *testing.T, rt *runtime.Runtime) error

type SQLAssertion struct {
	SQL   string
	Args  []any
	Count int
}

// createTestFlow creates a flow that starts with a split by contact id
// and then routes the contact to a node where all the actions in the
// test case are present.
//
// It returns the completed flow.
func createTestFlow(t *testing.T, uuid assets.FlowUUID, tc TestCase) flows.Flow {
	categoryUUIDs := make([]flows.CategoryUUID, len(tc.Actions))
	exitUUIDs := make([]flows.ExitUUID, len(tc.Actions))
	i := 0
	for range tc.Actions {
		categoryUUIDs[i] = flows.CategoryUUID(uuids.NewV4())
		exitUUIDs[i] = flows.ExitUUID(uuids.NewV4())
		i++
	}
	defaultCategoryUUID := flows.CategoryUUID(uuids.NewV4())
	defaultExitUUID := flows.ExitUUID(uuids.NewV4())

	cases := make([]*routers.Case, len(tc.Actions))
	categories := make([]flows.Category, len(tc.Actions))
	exits := make([]flows.Exit, len(tc.Actions))
	exitNodes := make([]flows.Node, len(tc.Actions))
	i = 0
	for contact, actions := range tc.Actions {
		cases[i] = routers.NewCase(
			uuids.NewV4(),
			"has_any_word",
			[]string{fmt.Sprintf("%d", contact.ID)},
			categoryUUIDs[i],
		)

		exitNodes[i] = definition.NewNode(
			flows.NewNodeUUID(),
			actions,
			nil,
			[]flows.Exit{definition.NewExit(flows.ExitUUID(uuids.NewV4()), "")},
		)

		categories[i] = routers.NewCategory(
			categoryUUIDs[i],
			fmt.Sprintf("Contact %d", contact.ID),
			exitUUIDs[i],
		)

		exits[i] = definition.NewExit(
			exitUUIDs[i],
			exitNodes[i].UUID(),
		)
		i++
	}

	// create our router
	categories = append(categories, routers.NewCategory(
		defaultCategoryUUID,
		"Other",
		defaultExitUUID,
	))
	exits = append(exits, definition.NewExit(
		defaultExitUUID,
		flows.NodeUUID(""),
	))

	router := routers.NewSwitch(nil, "", categories, "@contact.id", cases, defaultCategoryUUID)

	// and our entry node
	entry := definition.NewNode(
		flows.NewNodeUUID(),
		nil,
		router,
		exits,
	)

	nodes := []flows.Node{entry}
	nodes = append(nodes, exitNodes...)

	flowType := tc.FlowType
	if flowType == "" {
		flowType = flows.FlowTypeMessaging
	}

	// we have our nodes, lets create our flow
	flow, err := definition.NewFlow(
		uuid,
		"Test Flow",
		"eng",
		flowType,
		1,
		300,
		definition.NewLocalization(),
		nodes,
		nil,
		nil,
	)
	require.NoError(t, err)

	return flow
}

func RunTestCases(t *testing.T, ctx context.Context, rt *runtime.Runtime, tcs []TestCase) {
	models.FlushCache()

	oa, err := models.GetOrgAssets(ctx, rt, models.OrgID(1))
	assert.NoError(t, err)

	// reuse id from one of our real flows
	flowUUID := testdb.Favorites.UUID

	for i, tc := range tcs {
		if tc.Actions != nil {
			msgsByContactID := make(map[models.ContactID]*testdb.MsgIn)
			for contact, msg := range tc.Msgs {
				msgsByContactID[contact.ID] = msg
			}

			// create dynamic flow to test actions
			testFlow := createTestFlow(t, flowUUID, tc)
			flowDef, err := json.Marshal(testFlow)
			require.NoError(t, err)

			oa, err = oa.CloneForSimulation(ctx, rt, map[assets.FlowUUID][]byte{flowUUID: flowDef}, nil)
			assert.NoError(t, err)

			scenes := make([]*runner.Scene, 4)

			for i, c := range []*testdb.Contact{testdb.Cathy, testdb.Bob, testdb.George, testdb.Alexandra} {
				mc, contact, _ := c.Load(rt, oa)
				scenes[i] = runner.NewScene(mc, contact)
				if msg := msgsByContactID[c.ID]; msg != nil {
					scenes[i].IncomingMsg = &models.MsgInRef{ID: msg.ID}
					err := scenes[i].AddEvent(ctx, rt, oa, events.NewMsgReceived(msg.FlowMsg), models.NilUserID)
					require.NoError(t, err)
				}

				var trig flows.Trigger
				msg := msgsByContactID[c.ID]
				if msg != nil {
					msgEvt := events.NewMsgReceived(msg.FlowMsg)
					contact.SetLastSeenOn(msgEvt.CreatedOn())
					trig = triggers.NewBuilder(testFlow.Reference(false)).MsgReceived(msgEvt).Build()
				} else {
					trig = triggers.NewBuilder(testFlow.Reference(false)).Manual().Build()
				}

				err = scenes[i].StartSession(ctx, rt, oa, trig, true)
				require.NoError(t, err)
			}

			err = runner.BulkCommit(ctx, rt, oa, scenes)
			require.NoError(t, err)
		}
		if tc.Modifiers != nil {
			userID := models.NilUserID
			if tc.ModifierUser != nil {
				userID = tc.ModifierUser.ID
			}

			modifiersByContact := make(map[*flows.Contact][]flows.Modifier)
			for contact, mods := range tc.Modifiers {
				_, c, _ := contact.Load(rt, oa)
				modifiersByContact[c] = mods
			}

			_, err := runner.ApplyModifiers(ctx, rt, oa, userID, modifiersByContact)
			require.NoError(t, err)
		}

		// now check our assertions
		for j, a := range tc.SQLAssertions {
			assertdb.Query(t, rt.DB, a.SQL, a.Args...).Returns(a.Count, "%d:%d: mismatch in expected count for query: %s", i, j, a.SQL)
		}

		for j, a := range tc.Assertions {
			err := a(t, rt)
			assert.NoError(t, err, "%d:%d error checking assertion", i, j)
		}

		rt.Writers.History.Flush()

		persistedEvents := map[string]int{}
		for _, item := range dyntest.ScanAll[models.DynamoItem](t, rt.Dynamo, "TestHistory") {
			data, err := item.GetData()
			require.NoError(t, err)

			if eventType, ok := data["type"].(string); ok {
				persistedEvents[eventType]++
			}
		}

		assert.Equal(t, tc.PersistedEvents, persistedEvents, "%d: mismatch in persisted events", i)

		dyntest.Truncate(t, rt.Dynamo, "TestHistory")
	}
}
