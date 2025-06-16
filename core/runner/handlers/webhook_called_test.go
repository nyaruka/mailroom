package handlers_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/goflow/flows/engine"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookCalled(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)
	defer httpx.SetRequestor(httpx.DefaultRequestor)

	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"http://rapidpro.io/": {
			httpx.NewMockResponse(200, nil, []byte("OK")),
			httpx.NewMockResponse(200, nil, []byte("OK")),
		},
		"http://rapidpro.io/?unsub=1": {
			httpx.NewMockResponse(410, nil, []byte("Gone")),
			httpx.NewMockResponse(410, nil, []byte("Gone")),
			httpx.NewMockResponse(410, nil, []byte("Gone")),
		},
	}))

	// add a few resthooks
	rt.DB.MustExec(`INSERT INTO api_resthook(is_active, slug, org_id, created_on, modified_on, created_by_id, modified_by_id) VALUES(TRUE, 'foo', 1, NOW(), NOW(), 1, 1);`)
	rt.DB.MustExec(`INSERT INTO api_resthook(is_active, slug, org_id, created_on, modified_on, created_by_id, modified_by_id) VALUES(TRUE, 'bar', 1, NOW(), NOW(), 1, 1);`)

	// and a few targets
	rt.DB.MustExec(`INSERT INTO api_resthooksubscriber(is_active, created_on, modified_on, target_url, created_by_id, modified_by_id, resthook_id) VALUES(TRUE, NOW(), NOW(), 'http://rapidpro.io/', 1, 1, 1);`)
	rt.DB.MustExec(`INSERT INTO api_resthooksubscriber(is_active, created_on, modified_on, target_url, created_by_id, modified_by_id, resthook_id) VALUES(TRUE, NOW(), NOW(), 'http://rapidpro.io/?unsub=1', 1, 1, 2);`)
	rt.DB.MustExec(`INSERT INTO api_resthooksubscriber(is_active, created_on, modified_on, target_url, created_by_id, modified_by_id, resthook_id) VALUES(TRUE, NOW(), NOW(), 'http://rapidpro.io/?unsub=1', 1, 1, 1);`)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewCallResthook(handlers.NewActionUUID(), "foo", "foo"), // calls both subscribers
				},
				testdb.George: []flows.Action{
					actions.NewCallResthook(handlers.NewActionUUID(), "foo", "foo"), // calls both subscribers
					actions.NewCallWebhook(handlers.NewActionUUID(), "GET", "http://rapidpro.io/?unsub=1", nil, "", ""),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from api_resthooksubscriber where is_active = FALSE",
					Count: 1,
				},
				{
					SQL:   "select count(*) from api_resthooksubscriber where is_active = TRUE and resthook_id = $1",
					Args:  []any{2},
					Count: 1,
				},
				{
					SQL:   "select count(*) from api_resthooksubscriber where is_active = TRUE",
					Count: 2,
				},
				{
					SQL:   "select count(*) from request_logs_httplog where log_type = 'webhook_called' AND flow_id IS NOT NULL AND status_code = 200",
					Count: 2,
				},
				{
					SQL:   "select count(*) from request_logs_httplog where log_type = 'webhook_called' AND flow_id IS NOT NULL AND status_code = 410",
					Count: 3,
				},
				{
					SQL:   "select count(*) from api_webhookevent where org_id = $1",
					Args:  []any{testdb.Org1.ID},
					Count: 2,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}

// a webhook service which fakes slow responses
type failingWebhookService struct {
	delay time.Duration
}

func (s *failingWebhookService) Call(request *http.Request) (*httpx.Trace, error) {
	return &httpx.Trace{
		Request:       request,
		RequestTrace:  []byte(`GET http://rapidpro.io/`),
		Response:      nil,
		ResponseTrace: nil,
		StartTime:     dates.Now(),
		EndTime:       dates.Now().Add(s.delay),
	}, nil
}

func TestUnhealthyWebhookCalls(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)
	defer dates.SetNowFunc(time.Now)

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2021, 11, 17, 7, 0, 0, 0, time.UTC), time.Second))

	flowDef, err := os.ReadFile("testdata/webhook_flow.json")
	require.NoError(t, err)

	testdb.InsertFlow(rt, testdb.Org1, flowDef)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	env := envs.NewBuilder().Build()
	_, cathy, _ := testdb.Cathy.Load(rt, oa)

	// webhook service with a 2 second delay
	svc := &failingWebhookService{delay: 2 * time.Second}

	eng := engine.NewBuilder().WithWebhookServiceFactory(func(flows.SessionAssets) (flows.WebhookService, error) { return svc, nil }).Build()
	flowRef := assets.NewFlowReference("bc5d6b7b-3e18-4d7c-8279-50b460e74f7f", "Test")

	runFlowAndApplyEvents(t, ctx, rt, env, eng, oa, flowRef, cathy)
	runFlowAndApplyEvents(t, ctx, rt, env, eng, oa, flowRef, cathy)

	healthySeries := vkutil.NewIntervalSeries("webhooks:healthy", time.Minute*5, 4)
	unhealthySeries := vkutil.NewIntervalSeries("webhooks:unhealthy", time.Minute*5, 4)

	total, err := healthySeries.Total(ctx, rc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.NoError(t, err)
	assert.Equal(t, int64(2), total)

	total, err = unhealthySeries.Total(ctx, rc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// change webhook service delay to 30 seconds and re-run flow 9 times
	svc.delay = 30 * time.Second
	for i := 0; i < 9; i++ {
		runFlowAndApplyEvents(t, ctx, rt, env, eng, oa, flowRef, cathy)
	}

	// still no incident tho..
	total, _ = healthySeries.Total(ctx, rc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.Equal(t, int64(2), total)
	total, _ = unhealthySeries.Total(ctx, rc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.Equal(t, int64(9), total)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE incident_type = 'webhooks:unhealthy'`).Returns(0)

	// however 1 more bad call means this node is considered unhealthy
	runFlowAndApplyEvents(t, ctx, rt, env, eng, oa, flowRef, cathy)

	total, _ = healthySeries.Total(ctx, rc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.Equal(t, int64(2), total)
	total, _ = unhealthySeries.Total(ctx, rc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.Equal(t, int64(10), total)

	// and now we have an incident
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE incident_type = 'webhooks:unhealthy'`).Returns(1)

	var incidentID models.IncidentID
	rt.DB.Get(&incidentID, `SELECT id FROM notifications_incident`)

	// and a record of the nodes
	assertvk.SMembers(t, rc, fmt.Sprintf("incident:%d:nodes", incidentID), []string{"1bff8fe4-0714-433e-96a3-437405bf21cf"})

	// another bad call won't create another incident..
	runFlowAndApplyEvents(t, ctx, rt, env, eng, oa, flowRef, cathy)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE incident_type = 'webhooks:unhealthy'`).Returns(1)
	assertvk.SMembers(t, rc, fmt.Sprintf("incident:%d:nodes", incidentID), []string{"1bff8fe4-0714-433e-96a3-437405bf21cf"})
}

// start the given contact in the given flow using a specific engine instance
func runFlowAndApplyEvents(t *testing.T, ctx context.Context, rt *runtime.Runtime, env envs.Environment, eng flows.Engine, oa *models.OrgAssets, flowRef *assets.FlowReference, contact *flows.Contact) {
	trigger := triggers.NewBuilder(env, flowRef, contact).Manual().Build()

	fs, sprint, err := eng.NewSession(ctx, oa.SessionAssets(), trigger)
	require.NoError(t, err)

	tx, err := rt.DB.BeginTxx(ctx, nil)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	scene := runner.NewSessionScene(fs, sprint, nil)

	err = scene.AddEvents(ctx, rt, oa, sprint.Events())
	require.NoError(t, err)

	tx, err = rt.DB.BeginTxx(ctx, nil)
	require.NoError(t, err)

	err = runner.ExecutePreCommitHooks(ctx, rt, tx, oa, []*runner.Scene{scene})
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)
}
