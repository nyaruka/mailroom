package handlers_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/httpx"
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
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)
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
	rt.DB.MustExec(`INSERT INTO api_resthooksubscriber(is_active, created_on, modified_on, target_url, created_by_id, modified_by_id, resthook_id) VALUES(TRUE, NOW(), NOW(), 'http://rapidpro.io/', 1, 1, 30000);`)
	rt.DB.MustExec(`INSERT INTO api_resthooksubscriber(is_active, created_on, modified_on, target_url, created_by_id, modified_by_id, resthook_id) VALUES(TRUE, NOW(), NOW(), 'http://rapidpro.io/?unsub=1', 1, 1, 30001);`)
	rt.DB.MustExec(`INSERT INTO api_resthooksubscriber(is_active, created_on, modified_on, target_url, created_by_id, modified_by_id, resthook_id) VALUES(TRUE, NOW(), NOW(), 'http://rapidpro.io/?unsub=1', 1, 1, 30000);`)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewCallResthook(flows.NewActionUUID(), "foo", "foo"), // calls both subscribers
				},
				testdb.George: []flows.Action{
					actions.NewCallResthook(flows.NewActionUUID(), "foo", "foo"), // calls both subscribers
					actions.NewCallWebhook(flows.NewActionUUID(), "GET", "http://rapidpro.io/?unsub=1", nil, "", ""),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from api_resthooksubscriber where is_active = FALSE",
					Count: 1,
				},
				{
					SQL:   "select count(*) from api_resthooksubscriber where is_active = TRUE and resthook_id = 30001",
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
			PersistedEvents: map[string]int{"run_started": 4, "run_ended": 4},
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
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)
	defer dates.SetNowFunc(time.Now)

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2021, 11, 17, 7, 0, 0, 0, time.UTC), time.Second))

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/webhook_flow.json")
	flow := testFlows[0]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	mc, contact, _ := testdb.Cathy.Load(rt, oa)

	// webhook service with a 2 second delay
	svc := &failingWebhookService{delay: 2 * time.Second}
	eng := engine.NewBuilder().WithWebhookServiceFactory(func(flows.SessionAssets) (flows.WebhookService, error) { return svc, nil }).Build()

	runFlow := func() {
		scene := runner.NewScene(mc, contact)
		scene.Engine = func(r *runtime.Runtime) flows.Engine { return eng }

		err = scene.StartSession(ctx, rt, oa, triggers.NewBuilder(flow.Reference()).Manual().Build(), true)
		require.NoError(t, err)
		err = scene.Commit(ctx, rt, oa)
		require.NoError(t, err)
	}

	// start the flow twice
	for range 2 {
		runFlow()
	}

	healthySeries := vkutil.NewIntervalSeries("webhooks:healthy", time.Minute*5, 4)
	unhealthySeries := vkutil.NewIntervalSeries("webhooks:unhealthy", time.Minute*5, 4)

	total, err := healthySeries.Total(ctx, vc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.NoError(t, err)
	assert.Equal(t, int64(2), total)

	total, err = unhealthySeries.Total(ctx, vc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// change webhook service delay to 30 seconds and re-run flow 9 times
	svc.delay = 30 * time.Second

	for range 9 {
		runFlow()
	}

	// still no incident tho..
	total, _ = healthySeries.Total(ctx, vc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.Equal(t, int64(2), total)
	total, _ = unhealthySeries.Total(ctx, vc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.Equal(t, int64(9), total)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE incident_type = 'webhooks:unhealthy'`).Returns(0)

	// however 1 more bad call means this node is considered unhealthy
	runFlow()

	total, _ = healthySeries.Total(ctx, vc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.Equal(t, int64(2), total)
	total, _ = unhealthySeries.Total(ctx, vc, "1bff8fe4-0714-433e-96a3-437405bf21cf")
	assert.Equal(t, int64(10), total)

	// and now we have an incident
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE incident_type = 'webhooks:unhealthy'`).Returns(1)

	var incidentID models.IncidentID
	rt.DB.Get(&incidentID, `SELECT id FROM notifications_incident`)

	// and a record of the nodes
	assertvk.SMembers(t, vc, fmt.Sprintf("incident:%d:nodes", incidentID), []string{"1bff8fe4-0714-433e-96a3-437405bf21cf"})

	// another bad call won't create another incident..
	runFlow()

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE incident_type = 'webhooks:unhealthy'`).Returns(1)
	assertvk.SMembers(t, vc, fmt.Sprintf("incident:%d:nodes", incidentID), []string{"1bff8fe4-0714-433e-96a3-437405bf21cf"})
}
