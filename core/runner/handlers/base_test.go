package handlers

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
	FlowType      flows.FlowType
	Actions       ContactActionMap
	Msgs          ContactMsgMap
	Modifiers     ContactModifierMap
	ModifierUser  *testdb.User
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

			oa, err = oa.CloneForSimulation(ctx, rt, map[assets.FlowUUID]json.RawMessage{flowUUID: flowDef}, nil)
			assert.NoError(t, err)

			triggerBuilder := func(contact *flows.Contact) flows.Trigger {
				msg := msgsByContactID[models.ContactID(contact.ID())]
				if msg == nil {
					return triggers.NewBuilder(oa.Env(), testFlow.Reference(false), contact).Manual().Build()
				}
				return triggers.NewBuilder(oa.Env(), testFlow.Reference(false), contact).Msg(msg.FlowMsg).Build()
			}

			for _, c := range []*testdb.Contact{testdb.Cathy, testdb.Bob, testdb.George, testdb.Alexandra} {
				sceneInit := func(scene *runner.Scene) {
					if msg := msgsByContactID[c.ID]; msg != nil {
						scene.IncomingMsg = &models.MsgInRef{ID: msg.ID}
					}
				}

				_, err := runner.StartWithLock(ctx, rt, oa, []models.ContactID{c.ID}, triggerBuilder, true, models.NilStartID, sceneInit)
				require.NoError(t, err)
			}
		}
		if tc.Modifiers != nil {
			userID := models.NilUserID
			if tc.ModifierUser != nil {
				userID = tc.ModifierUser.ID
			}

			modifiersByContact := make(map[*flows.Contact][]flows.Modifier)
			for contact, mods := range tc.Modifiers {
				_, fc, _ := contact.Load(rt, oa)
				modifiersByContact[fc] = mods
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
	}
}
