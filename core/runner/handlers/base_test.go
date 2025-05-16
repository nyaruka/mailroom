package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/definition"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/goflow/flows/routers"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ContactActionMap map[*testdata.Contact][]flows.Action
type ContactMsgMap map[*testdata.Contact]*testdata.MsgIn
type ContactModifierMap map[*testdata.Contact][]flows.Modifier

type modifyResult struct {
	Contact *flows.Contact `json:"contact"`
	Events  []flows.Event  `json:"events"`
}

type TestCase struct {
	FlowType      flows.FlowType
	Actions       ContactActionMap
	Msgs          ContactMsgMap
	Modifiers     ContactModifierMap
	ModifierUser  *testdata.User
	Assertions    []Assertion
	SQLAssertions []SQLAssertion
}

type Assertion func(t *testing.T, rt *runtime.Runtime) error

type SQLAssertion struct {
	SQL   string
	Args  []any
	Count int
}

func NewActionUUID() flows.ActionUUID {
	return flows.ActionUUID(uuids.NewV4())
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
			flows.NodeUUID(uuids.NewV4()),
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
		flows.NodeUUID(uuids.NewV4()),
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

	eng := goflow.Engine(rt)

	// reuse id from one of our real flows
	flowUUID := testdata.Favorites.UUID

	for i, tc := range tcs {
		msgsByContactID := make(map[models.ContactID]*testdata.MsgIn)
		for contact, msg := range tc.Msgs {
			msgsByContactID[contact.ID] = msg
		}

		// build our flow for this test case
		testFlow := createTestFlow(t, flowUUID, tc)
		flowDef, err := json.Marshal(testFlow)
		require.NoError(t, err)

		oa, err = oa.CloneForSimulation(ctx, rt, map[assets.FlowUUID]json.RawMessage{flowUUID: flowDef}, nil)
		assert.NoError(t, err)

		flow, err := oa.FlowByUUID(flowUUID)
		require.NoError(t, err)

		options := &runner.StartOptions{
			Interrupt: true,
			TriggerBuilder: func(contact *flows.Contact) flows.Trigger {
				msg := msgsByContactID[models.ContactID(contact.ID())]
				if msg == nil {
					return triggers.NewBuilder(oa.Env(), testFlow.Reference(false), contact).Manual().Build()
				}
				return triggers.NewBuilder(oa.Env(), testFlow.Reference(false), contact).Msg(msg.FlowMsg).Build()
			},
		}

		for _, c := range []*testdata.Contact{testdata.Cathy, testdata.Bob, testdata.George, testdata.Alexandria} {
			sceneInit := func(scene *runner.Scene) {
				if msg := msgsByContactID[c.ID]; msg != nil {
					scene.IncomingMsg = &models.MsgInRef{ID: msg.ID}
				}
			}

			_, err := runner.StartFlowWithLock(ctx, rt, oa, flow.(*models.Flow), []models.ContactID{c.ID}, options, models.NilStartID, sceneInit)
			require.NoError(t, err)
		}

		results := make(map[models.ContactID]modifyResult)

		// create scenes for our contacts
		scenes := make([]*runner.Scene, 0, len(tc.Modifiers))
		for contact, mods := range tc.Modifiers {
			contact, err := models.LoadContact(ctx, rt.DB, oa, contact.ID)
			assert.NoError(t, err)

			flowContact, err := contact.FlowContact(oa)
			assert.NoError(t, err)

			result := modifyResult{
				Contact: flowContact,
				Events:  make([]flows.Event, 0, len(mods)),
			}

			scene := runner.NewNonFlowScene(flowContact, tc.ModifierUser.SafeID(), nil)

			// apply our modifiers
			for _, mod := range mods {
				modifiers.Apply(eng, oa.Env(), oa.SessionAssets(), flowContact, mod, func(e flows.Event) { result.Events = append(result.Events, e) })
			}

			results[contact.ID()] = result
			scenes = append(scenes, scene)

		}

		for _, scene := range scenes {
			err := scene.AddEvents(ctx, rt, oa, results[scene.ContactID()].Events)
			assert.NoError(t, err)
		}

		tx, err := rt.DB.BeginTxx(ctx, nil)
		assert.NoError(t, err)

		err = runner.ExecutePreCommitHooks(ctx, rt, tx, oa, scenes)
		assert.NoError(t, err)

		err = tx.Commit()
		assert.NoError(t, err)

		err = runner.ExecutePostCommitHooks(ctx, rt, oa, scenes)
		assert.NoError(t, err)

		// now check our assertions
		for j, a := range tc.SQLAssertions {
			assertdb.Query(t, rt.DB, a.SQL, a.Args...).Returns(a.Count, "%d:%d: mismatch in expected count for query: %s", i, j, a.SQL)
		}

		for j, a := range tc.Assertions {
			err := a(t, rt)
			assert.NoError(t, err, "%d:%d error checking assertion", i, j)
		}
	}
}

func RunFlowAndApplyEvents(t *testing.T, ctx context.Context, rt *runtime.Runtime, env envs.Environment, eng flows.Engine, oa *models.OrgAssets, flowRef *assets.FlowReference, contact *flows.Contact) {
	trigger := triggers.NewBuilder(env, flowRef, contact).Manual().Build()
	fs, sprint, err := eng.NewSession(ctx, oa.SessionAssets(), trigger)
	require.NoError(t, err)

	tx, err := rt.DB.BeginTxx(ctx, nil)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	scene := runner.NewSessionScene(fs, sprint, 0, nil)

	err = scene.AddEvents(ctx, rt, oa, sprint.Events())
	require.NoError(t, err)

	tx, err = rt.DB.BeginTxx(ctx, nil)
	require.NoError(t, err)

	err = runner.ExecutePreCommitHooks(ctx, rt, tx, oa, []*runner.Scene{scene})
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)
}
