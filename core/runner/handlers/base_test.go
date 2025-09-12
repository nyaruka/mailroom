package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

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
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ContactActionMap map[flows.ContactUUID][]flows.Action
type ContactMsgMap map[flows.ContactUUID]*testdb.MsgIn
type ContactModifierMap map[flows.ContactUUID][]flows.Modifier

type TestCase struct {
	Actions         ContactActionMap
	Msgs            ContactMsgMap
	Modifiers       ContactModifierMap
	UserID          models.UserID
	SQLAssertions   []SQLAssertion
	ExpectedTasks   map[string][]string
	PersistedEvents map[flows.ContactUUID][]string
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
	for contactUUID, actions := range tc.Actions {
		cases[i] = routers.NewCase(uuids.NewV4(), "has_any_word", []string{string(contactUUID)}, categoryUUIDs[i])

		exitNodes[i] = definition.NewNode(
			flows.NewNodeUUID(),
			actions,
			nil,
			[]flows.Exit{definition.NewExit(flows.ExitUUID(uuids.NewV4()), "")},
		)

		categories[i] = routers.NewCategory(categoryUUIDs[i], fmt.Sprintf("Contact %s", contactUUID), exitUUIDs[i])

		exits[i] = definition.NewExit(exitUUIDs[i], exitNodes[i].UUID())
		i++
	}

	// create our router
	categories = append(categories, routers.NewCategory(defaultCategoryUUID, "Other", defaultExitUUID))
	exits = append(exits, definition.NewExit(defaultExitUUID, flows.NodeUUID("")))
	router := routers.NewSwitch(nil, "", categories, "@contact.uuid", cases, defaultCategoryUUID)

	// and our entry node
	entry := definition.NewNode(flows.NewNodeUUID(), nil, router, exits)

	nodes := []flows.Node{entry}
	nodes = append(nodes, exitNodes...)

	// we have our nodes, lets create our flow
	flow, err := definition.NewFlow(
		uuid,
		"Test Flow",
		"eng",
		flows.FlowTypeMessaging,
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

func runTestCases(t *testing.T, ctx context.Context, rt *runtime.Runtime, tcs []TestCase, reset testsuite.ResetFlag) {
	models.FlushCache()

	oa, err := models.GetOrgAssets(ctx, rt, models.OrgID(1))
	assert.NoError(t, err)

	// reuse id from one of our real flows
	flowUUID := testdb.Favorites.UUID

	for i, tc := range tcs {
		if tc.Actions != nil {
			// create dynamic flow to test actions
			testFlow := createTestFlow(t, flowUUID, tc)
			flowDef, err := json.Marshal(testFlow)
			require.NoError(t, err)

			oa, err = oa.CloneForSimulation(ctx, rt, map[assets.FlowUUID][]byte{flowUUID: flowDef}, nil)
			assert.NoError(t, err)

			scenes := make([]*runner.Scene, 4)

			for i, c := range []*testdb.Contact{testdb.Ann, testdb.Bob, testdb.Cat, testdb.Dan} {
				mc, contact, _ := c.Load(rt, oa)
				scenes[i] = runner.NewScene(mc, contact)
				if msg := tc.Msgs[c.UUID]; msg != nil {
					scenes[i].IncomingMsg = &models.MsgInRef{ID: msg.ID}
					err := scenes[i].AddEvent(ctx, rt, oa, events.NewMsgReceived(msg.FlowMsg), models.NilUserID)
					require.NoError(t, err)
				}

				var trig flows.Trigger
				msg := tc.Msgs[c.UUID]
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
			modifiersByContact := make(map[*flows.Contact][]flows.Modifier)
			for _, c := range []*testdb.Contact{testdb.Ann, testdb.Bob, testdb.Cat, testdb.Dan} {
				_, contact, _ := c.Load(rt, oa)

				modifiersByContact[contact] = tc.Modifiers[c.UUID]

			}

			_, err := runner.BulkModify(ctx, rt, oa, tc.UserID, modifiersByContact)
			require.NoError(t, err)
		}

		// clone test case and populate with actual values
		actual := tc
		actual.ExpectedTasks = testsuite.GetQueuedTasks(t, rt)
		actual.PersistedEvents = testsuite.GetHistoryEventTypes(t, rt)

		// now check our assertions
		for j, a := range tc.SQLAssertions {
			assertdb.Query(t, rt.DB, a.SQL, a.Args...).Returns(a.Count, "%d:%d: mismatch in expected count for query: %s", i, j, a.SQL)
		}

		if tc.ExpectedTasks == nil {
			tc.ExpectedTasks = make(map[string][]string)
		}
		assert.Equal(t, tc.ExpectedTasks, actual.ExpectedTasks, "%d: unexpected tasks", i)

		assert.Equal(t, tc.PersistedEvents, actual.PersistedEvents, "%d: mismatch in persisted events", i)

		if reset != 0 {
			testsuite.Reset(t, rt, reset)
		}
	}
}
