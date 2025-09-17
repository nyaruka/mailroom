package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestTicketOpened(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)
	defer httpx.SetRequestor(httpx.DefaultRequestor)

	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://api.mailgun.net/v3/tickets.rapidpro.io/messages": {
			httpx.NewMockResponse(200, nil, []byte(`{
				"id": "<20200426161758.1.590432020254B2BF@tickets.rapidpro.io>",
				"message": "Queued. Thank you."
			}`)),
		},
		"https://nyaruka.zendesk.com/api/v2/any_channel/push.json": {
			httpx.NewMockResponse(201, nil, []byte(`{
				"results": [
					{
						"external_resource_id": "123",
						"status": {"code": "success"}
					}
				]
			}`)),
		},
	}))

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewOpenTicket(
						flows.NewActionUUID(),
						assets.NewTopicReference(testdb.SupportTopic.UUID, "Support"),
						"Where are my cookies?",
						assets.NewUserReference("e29fdf9f-56ab-422a-b77d-e3ec26091a25", "Admin"),
					),
				},
				testdb.Bob.UUID: []flows.Action{
					actions.NewOpenTicket(
						flows.NewActionUUID(),
						nil,
						"I've found some cookies",
						nil,
					),
				},
			},
			DBAssertions: []assertdb.Assert{
				{ // Ann's old ticket will still be open and Ann's new ticket will have been created
					Query:   "select count(*) from tickets_ticket where contact_id = $1 AND status = 'O'",
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
				{ // bob's ticket will have been created too
					Query:   "select count(*) from tickets_ticket where contact_id = $1 AND status = 'O'",
					Args:    []any{testdb.Bob.ID},
					Returns: 1,
				},
				{ // and we have 2 ticket opened events for the 2 tickets opened
					Query:   "select count(*) from tickets_ticketevent where event_type = 'O'",
					Returns: 2,
				},
				{ // both of our tickets have a topic (one without an explicit topic get's the default)
					Query:   "select count(*) from tickets_ticket where topic_id is null",
					Returns: 0,
				},
				{ // one of our tickets is assigned to admin
					Query:   "select count(*) from tickets_ticket where assignee_id = $1",
					Args:    []any{testdb.Admin.ID},
					Returns: 1,
				},
				{ // admin will have a ticket assigned notification for the ticket directly assigned to them
					Query:   "select count(*) from notifications_notification where user_id = $1 and notification_type = 'tickets:activity'",
					Args:    []any{testdb.Admin.ID},
					Returns: 1,
				},
				{ // all assignable users will have a ticket opened notification for the unassigned ticket
					Query:   "select count(*) from notifications_notification where notification_type = 'tickets:opened'",
					Args:    nil,
					Returns: 3,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "ticket_opened", "contact_groups_changed", "run_ended"},
				testdb.Bob.UUID: {"run_started", "ticket_opened", "contact_groups_changed", "run_ended"},
				testdb.Cat.UUID: {"run_started", "run_ended"},
				testdb.Dan.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo)
}
