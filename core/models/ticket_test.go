package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTickets(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa := testdb.Org1.Load(t, rt)

	ticket1 := models.NewTicket(
		"2ef57efc-d85f-4291-b330-e4afe68af5fe",
		testdb.Org1.ID,
		testdb.Admin.ID,
		models.NilFlowID,
		testdb.Ann.ID,
		testdb.DefaultTopic.ID,
		testdb.Admin.ID,
	)
	ticket2 := models.NewTicket(
		"64f81be1-00ff-48ef-9e51-97d6f924c1a4",
		testdb.Org1.ID,
		testdb.Admin.ID,
		models.NilFlowID,
		testdb.Bob.ID,
		testdb.SalesTopic.ID,
		models.NilUserID,
	)
	ticket3 := models.NewTicket(
		"28ef8ddc-b221-42f3-aeae-ee406fc9d716",
		testdb.Org1.ID,
		models.NilUserID,
		testdb.Favorites.ID,
		testdb.Dan.ID,
		testdb.SupportTopic.ID,
		testdb.Admin.ID,
	)

	assert.Equal(t, flows.TicketUUID("2ef57efc-d85f-4291-b330-e4afe68af5fe"), ticket1.UUID)
	assert.Equal(t, testdb.Org1.ID, ticket1.OrgID)
	assert.Equal(t, testdb.Ann.ID, ticket1.ContactID)
	assert.Equal(t, testdb.DefaultTopic.ID, ticket1.TopicID)
	assert.Equal(t, testdb.Admin.ID, ticket1.AssigneeID)

	err := models.InsertTickets(ctx, rt.DB, oa, []*models.Ticket{ticket1, ticket2, ticket3})
	assert.NoError(t, err)

	// check all tickets were created
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE status = 'O' AND closed_on IS NULL`).Returns(3)

	// check counts were added
	today := time.Now().In(oa.Env().Timezone()).Format("2006-01-02")
	testsuite.AssertDailyCounts(t, rt, testdb.Org1, map[string]int{
		today + "/tickets:opened:10000": 1,
		today + "/tickets:opened:10001": 1,
		today + "/tickets:opened:10002": 1,
		today + "/tickets:assigned:0:3": 2,
	})
	testsuite.AssertDailyCounts(t, rt, testdb.Org2, map[string]int{})
}

func TestUpdateTicketLastActivity(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	now := time.Date(2021, 6, 22, 15, 59, 30, 123456000, time.UTC)

	defer dates.SetNowFunc(time.Now)
	dates.SetNowFunc(dates.NewFixedNow(now))

	ticket := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), nil)
	modelTicket := ticket.Load(t, rt, testdb.Org1)

	models.UpdateTicketLastActivity(ctx, rt.DB, []*models.Ticket{modelTicket})

	assert.Equal(t, now, modelTicket.LastActivityOn)

	assertdb.Query(t, rt.DB, `SELECT last_activity_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(modelTicket.LastActivityOn)
}

func TestUpdateTickets(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	ticket1 := testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.SalesTopic, nil).Load(t, rt, testdb.Org1)
	ticket2 := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.SalesTopic, time.Now(), nil).Load(t, rt, testdb.Org1)
	ticket3 := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Admin).Load(t, rt, testdb.Org1)

	assertTicket := func(tk *models.Ticket, cols map[string]any) {
		assertdb.Query(t, rt.DB, `SELECT status, assignee_id, topic_id FROM tickets_ticket WHERE id = $1`, tk.ID).Columns(cols)
	}

	assertTicket(ticket1, map[string]any{"status": "C", "assignee_id": nil, "topic_id": testdb.SalesTopic.ID})
	assertTicket(ticket2, map[string]any{"status": "O", "assignee_id": nil, "topic_id": testdb.SalesTopic.ID})
	assertTicket(ticket3, map[string]any{"status": "O", "assignee_id": testdb.Admin.ID, "topic_id": testdb.DefaultTopic.ID})

	// update with no changes
	err := models.UpdateTickets(ctx, rt.DB, []*models.Ticket{ticket1, ticket2, ticket3})
	assert.NoError(t, err)

	assertTicket(ticket1, map[string]any{"status": "C", "assignee_id": nil, "topic_id": testdb.SalesTopic.ID})
	assertTicket(ticket2, map[string]any{"status": "O", "assignee_id": nil, "topic_id": testdb.SalesTopic.ID})
	assertTicket(ticket3, map[string]any{"status": "O", "assignee_id": testdb.Admin.ID, "topic_id": testdb.DefaultTopic.ID})

	ticket1.AssigneeID = testdb.Agent.ID
	ticket2.TopicID = testdb.SupportTopic.ID
	ticket3.Status = models.TicketStatusClosed
	ticket3.AssigneeID = models.NilUserID
	ticket3.LastActivityOn = time.Date(2025, 9, 3, 16, 0, 0, 0, time.UTC)

	err = models.UpdateTickets(ctx, rt.DB, []*models.Ticket{ticket1, ticket2, ticket3})
	assert.NoError(t, err)

	assertTicket(ticket1, map[string]any{"status": "C", "assignee_id": testdb.Agent.ID, "topic_id": testdb.SalesTopic.ID})
	assertTicket(ticket2, map[string]any{"status": "O", "assignee_id": nil, "topic_id": testdb.SupportTopic.ID})
	assertTicket(ticket3, map[string]any{"status": "C", "assignee_id": nil, "topic_id": testdb.DefaultTopic.ID})
}

func TestTicketRecordReply(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	openedOn := time.Date(2022, 5, 17, 14, 21, 0, 0, time.UTC)
	repliedOn := time.Date(2022, 5, 18, 15, 0, 0, 0, time.UTC)

	ticket := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, openedOn, nil)

	err = models.RecordTicketReply(ctx, rt.DB, oa, ticket.ID, testdb.Agent.ID, repliedOn)
	assert.NoError(t, err)

	modelTicket := ticket.Load(t, rt, testdb.Org1)
	assert.Equal(t, repliedOn, *modelTicket.RepliedOn)
	assert.Equal(t, repliedOn, modelTicket.LastActivityOn)

	assertdb.Query(t, rt.DB, `SELECT replied_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(repliedOn)
	assertdb.Query(t, rt.DB, `SELECT last_activity_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(repliedOn)

	// check counts were added
	openYmd := openedOn.In(oa.Env().Timezone()).Format("2006-01-02")
	replyYmd := repliedOn.In(oa.Env().Timezone()).Format("2006-01-02")
	testsuite.AssertDailyCounts(t, rt, testdb.Org1, map[string]int{
		replyYmd + "/msgs:ticketreplies:10001:5": 1,
		openYmd + "/ticketresptime:total":        88740,
		openYmd + "/ticketresptime:count":        1,
	})
	testsuite.AssertDailyCounts(t, rt, testdb.Org2, map[string]int{})

	repliedAgainOn := time.Date(2022, 5, 18, 15, 5, 0, 0, time.UTC)

	// if we call it again, it won't change replied_on again but it will update last_activity_on
	err = models.RecordTicketReply(ctx, rt.DB, oa, ticket.ID, testdb.Agent.ID, repliedAgainOn)
	assert.NoError(t, err)

	modelTicket = ticket.Load(t, rt, testdb.Org1)
	assert.Equal(t, repliedOn, *modelTicket.RepliedOn)
	assert.Equal(t, repliedAgainOn, modelTicket.LastActivityOn)

	assertdb.Query(t, rt.DB, `SELECT replied_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(repliedOn)
	assertdb.Query(t, rt.DB, `SELECT last_activity_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(repliedAgainOn)

	testsuite.AssertDailyCounts(t, rt, testdb.Org1, map[string]int{
		replyYmd + "/msgs:ticketreplies:10001:5": 2,
		openYmd + "/ticketresptime:total":        88740,
		openYmd + "/ticketresptime:count":        1,
	})
}
