package handlers_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/goflow/flows/definition"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/routers"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ContactActionMap map[flows.ContactUUID][]flows.Action

func (m *ContactActionMap) UnmarshalJSON(d []byte) error {
	*m = make(ContactActionMap)

	var raw map[flows.ContactUUID][]json.RawMessage
	if err := json.Unmarshal(d, &raw); err != nil {
		return err
	}

	for contactUUID, v := range raw {
		unmarshaled := make([]flows.Action, len(v))
		for i := range v {
			var err error
			unmarshaled[i], err = actions.Read(v[i])
			if err != nil {
				return err
			}
		}
		(*m)[contactUUID] = unmarshaled
	}
	return nil
}

type ContactModifierMap map[flows.ContactUUID][]flows.Modifier

type TestCase struct {
	Label           string                             `json:"label"`
	Msgs            map[flows.ContactUUID]*flows.MsgIn `json:"msgs,omitempty"`
	Actions         ContactActionMap                   `json:"actions,omitempty"`
	Modifiers       ContactModifierMap                 `json:"modifiers,omitempty"`
	UserID          models.UserID                      `json:"user_id,omitempty"`
	DBAssertions    []assertdb.Assert                  `json:"db_assertions,omitempty"`
	ExpectedTasks   map[string][]string                `json:"expected_tasks,omitempty"`
	PersistedEvents map[flows.ContactUUID][]string     `json:"persisted_events,omitempty"`
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

func runTests(t *testing.T, rt *runtime.Runtime, truthFile string) {
	ctx := t.Context()
	tcs := make([]TestCase, 0, 20)
	tcJSON := testsuite.ReadFile(t, truthFile)

	jsonx.MustUnmarshal(tcJSON, &tcs)

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
				mc, contact, _ := c.Load(t, rt, oa)
				scenes[i] = runner.NewScene(mc, contact)

				msg := tc.Msgs[c.UUID]
				var trig flows.Trigger

				if msg != nil {
					msgEvent := events.NewMsgReceived(msg)
					scenes[i].IncomingMsg = insertTestMessage(t, rt, oa, c, msg)
					err := scenes[i].AddEvent(ctx, rt, oa, msgEvent, models.NilUserID)
					require.NoError(t, err)

					contact.SetLastSeenOn(msgEvent.CreatedOn())
					trig = triggers.NewBuilder(testFlow.Reference(false)).MsgReceived(msgEvent).Build()
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
				_, contact, _ := c.Load(t, rt, oa)

				modifiersByContact[contact] = tc.Modifiers[c.UUID]

			}

			_, err := runner.BulkModify(ctx, rt, oa, tc.UserID, modifiersByContact)
			require.NoError(t, err)
		}

		// clone test case and populate with actual values
		actual := tc
		actual.ExpectedTasks = testsuite.GetQueuedTaskTypes(t, rt)
		actual.PersistedEvents = testsuite.GetHistoryEventTypes(t, rt, true)

		testsuite.ClearTasks(t, rt)

		if !test.UpdateSnapshots {
			// now check our assertions
			for _, dba := range tc.DBAssertions {
				dba.Check(t, rt.DB, "%s: assertion for query '%s' failed", tc.Label, dba.Query)
			}

			if tc.ExpectedTasks == nil {
				tc.ExpectedTasks = make(map[string][]string)
			}
			assert.Equal(t, tc.ExpectedTasks, actual.ExpectedTasks, "%s: unexpected tasks", tc.Label)

			assert.Equal(t, tc.PersistedEvents, actual.PersistedEvents, "%s: mismatch in persisted events", tc.Label)
		} else {
			tcs[i] = actual
		}
	}

	// update if we are meant to
	if test.UpdateSnapshots {
		truth, err := jsonx.MarshalPretty(tcs)
		require.NoError(t, err)

		err = os.WriteFile(truthFile, truth, 0644)
		require.NoError(t, err, "failed to update truth file")
	}
}

func insertTestMessage(t *testing.T, rt *runtime.Runtime, oa *models.OrgAssets, c *testdb.Contact, msg *flows.MsgIn) *models.MsgInRef {
	ch := oa.ChannelByUUID(msg.Channel().UUID)
	tch := &testdb.Channel{ID: ch.ID(), UUID: ch.UUID(), Type: ch.Type()}

	m := testdb.InsertIncomingMsg(t, rt, testdb.Org1, tch, c, msg.Text(), models.MsgStatusPending)
	return &models.MsgInRef{ID: m.ID}
}
