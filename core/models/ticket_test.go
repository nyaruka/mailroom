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
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa := testdb.Org1.Load(rt)

	ticket1 := models.NewTicket(
		"2ef57efc-d85f-4291-b330-e4afe68af5fe",
		testdb.Org1.ID,
		testdb.Admin.ID,
		models.NilFlowID,
		testdb.Cathy.ID,
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
		testdb.Alexandra.ID,
		testdb.SupportTopic.ID,
		testdb.Admin.ID,
	)

	assert.Equal(t, flows.TicketUUID("2ef57efc-d85f-4291-b330-e4afe68af5fe"), ticket1.UUID())
	assert.Equal(t, testdb.Org1.ID, ticket1.OrgID())
	assert.Equal(t, testdb.Cathy.ID, ticket1.ContactID())
	assert.Equal(t, testdb.DefaultTopic.ID, ticket1.TopicID())
	assert.Equal(t, testdb.Admin.ID, ticket1.AssigneeID())

	err := models.InsertTickets(ctx, rt.DB, oa, []*models.Ticket{ticket1, ticket2, ticket3})
	assert.NoError(t, err)

	// check all tickets were created
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE status = 'O' AND closed_on IS NULL`).Returns(3)

	// check counts were added
	today := time.Now().In(oa.Env().Timezone()).Format("2006-01-02")
	testsuite.AssertDailyCounts(t, rt, testdb.Org1, map[string]int{
		today + "/tickets:opened:1":     1,
		today + "/tickets:opened:2":     1,
		today + "/tickets:opened:3":     1,
		today + "/tickets:assigned:0:4": 2,
	})
	testsuite.AssertDailyCounts(t, rt, testdb.Org2, map[string]int{})

	// can lookup a ticket by UUID
	tk, err := models.LookupTicketByUUID(ctx, rt.DB, "64f81be1-00ff-48ef-9e51-97d6f924c1a4")
	assert.NoError(t, err)
	assert.Equal(t, flows.TicketUUID("64f81be1-00ff-48ef-9e51-97d6f924c1a4"), tk.UUID())
	assert.Equal(t, testdb.Bob.ID, tk.ContactID())

	// can lookup open tickets by contact
	org1, _ := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	cathy, err := models.LoadContact(ctx, rt.DB, org1, testdb.Cathy.ID)
	require.NoError(t, err)

	tk, err = models.LoadOpenTicketForContact(ctx, rt.DB, cathy)
	assert.NoError(t, err)
	assert.Equal(t, flows.TicketUUID("2ef57efc-d85f-4291-b330-e4afe68af5fe"), tk.UUID())
	assert.Equal(t, testdb.Cathy.ID, tk.ContactID())
}

func TestUpdateTicketLastActivity(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	now := time.Date(2021, 6, 22, 15, 59, 30, 123456000, time.UTC)

	defer dates.SetNowFunc(time.Now)
	dates.SetNowFunc(dates.NewFixedNow(now))

	ticket := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)
	modelTicket := ticket.Load(rt)

	models.UpdateTicketLastActivity(ctx, rt.DB, []*models.Ticket{modelTicket})

	assert.Equal(t, now, modelTicket.LastActivityOn())

	assertdb.Query(t, rt.DB, `SELECT last_activity_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(modelTicket.LastActivityOn())

}

func TestTicketsAssign(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	ticket1 := testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)
	modelTicket1 := ticket1.Load(rt)

	ticket2 := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)
	modelTicket2 := ticket2.Load(rt)

	// create ticket already assigned to a user
	ticket3 := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Admin)
	modelTicket3 := ticket3.Load(rt)

	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)

	evts, err := models.TicketsAssign(ctx, rt.DB, oa, testdb.Admin.ID, []*models.Ticket{modelTicket1, modelTicket2, modelTicket3}, testdb.Agent.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(evts))
	assert.Equal(t, models.TicketEventTypeAssigned, evts[modelTicket1].EventType())
	assert.Equal(t, models.TicketEventTypeAssigned, evts[modelTicket2].EventType())
	assert.Equal(t, models.TicketEventTypeAssigned, evts[modelTicket3].EventType())

	// check tickets are now assigned
	assertdb.Query(t, rt.DB, `SELECT assignee_id FROM tickets_ticket WHERE id = $1`, ticket1.ID).Columns(map[string]any{"assignee_id": int64(testdb.Agent.ID)})
	assertdb.Query(t, rt.DB, `SELECT assignee_id FROM tickets_ticket WHERE id = $1`, ticket2.ID).Columns(map[string]any{"assignee_id": int64(testdb.Agent.ID)})
	assertdb.Query(t, rt.DB, `SELECT assignee_id FROM tickets_ticket WHERE id = $1`, ticket3.ID).Columns(map[string]any{"assignee_id": int64(testdb.Agent.ID)})

	// and there are new assigned events with notifications
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent WHERE event_type = 'A'`).Returns(3)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_notification WHERE user_id = $1 AND notification_type = 'tickets:activity'`, testdb.Agent.ID).Returns(1)

	// and daily counts (we only count first assignments of a ticket)
	today := time.Now().In(oa.Env().Timezone()).Format("2006-01-02")
	testsuite.AssertDailyCounts(t, rt, testdb.Org1, map[string]int{
		today + "/tickets:assigned:2:6": 2,
	})
	testsuite.AssertDailyCounts(t, rt, testdb.Org2, map[string]int{})
}

func TestTicketsAddNote(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	ticket1 := testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)
	modelTicket1 := ticket1.Load(rt)

	ticket2 := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Agent)
	modelTicket2 := ticket2.Load(rt)

	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)

	evts, err := models.TicketsAddNote(ctx, rt.DB, oa, testdb.Admin.ID, []*models.Ticket{modelTicket1, modelTicket2}, "spam")
	require.NoError(t, err)
	assert.Equal(t, 2, len(evts))
	assert.Equal(t, models.TicketEventTypeNoteAdded, evts[modelTicket1].EventType())
	assert.Equal(t, models.TicketEventTypeNoteAdded, evts[modelTicket2].EventType())

	// check there are new note events
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent WHERE event_type = 'N' AND note = 'spam'`).Returns(2)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_notification WHERE user_id = $1 AND notification_type = 'tickets:activity'`, testdb.Agent.ID).Returns(1)
}

func TestTicketsChangeTopic(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	ticket1 := testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.SalesTopic, nil)
	modelTicket1 := ticket1.Load(rt)

	ticket2 := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.SupportTopic, time.Now(), nil)
	modelTicket2 := ticket2.Load(rt)

	ticket3 := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)
	modelTicket3 := ticket3.Load(rt)

	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)

	evts, err := models.TicketsChangeTopic(ctx, rt.DB, oa, testdb.Admin.ID, []*models.Ticket{modelTicket1, modelTicket2, modelTicket3}, testdb.SupportTopic.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(evts)) // ticket 2 not included as already has that topic
	assert.Equal(t, models.TicketEventTypeTopicChanged, evts[modelTicket1].EventType())
	assert.Equal(t, models.TicketEventTypeTopicChanged, evts[modelTicket3].EventType())

	// check tickets are updated and we have events
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE topic_id = $1`, testdb.SupportTopic.ID).Returns(3)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent WHERE event_type = 'T' AND topic_id = $1`, testdb.SupportTopic.ID).Returns(2)
}

func TestCloseTickets(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	ticket1 := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)
	modelTicket1 := ticket1.Load(rt)

	ticket2 := testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)
	modelTicket2 := ticket2.Load(rt)

	_, cathy, _ := testdb.Cathy.Load(rt, oa)

	err = models.CalculateDynamicGroups(ctx, rt.DB, oa, []*flows.Contact{cathy})
	require.NoError(t, err)

	assert.Equal(t, "Doctors", cathy.Groups().All()[0].Name())
	assert.Equal(t, "Open Tickets", cathy.Groups().All()[1].Name())

	evts, err := models.CloseTickets(ctx, rt, oa, testdb.Admin.ID, []*models.Ticket{modelTicket1, modelTicket2})
	require.NoError(t, err)
	assert.Equal(t, 1, len(evts))
	assert.Equal(t, models.TicketEventTypeClosed, evts[modelTicket1].EventType())

	// check ticket #1 is now closed
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE id = $1 AND status = 'C' AND closed_on IS NOT NULL`, ticket1.ID).Returns(1)

	// and there's closed event for it
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent WHERE org_id = $1 AND ticket_id = $2 AND event_type = 'C'`,
		[]any{testdb.Org1.ID, ticket1.ID}, 1)

	// reload Cathy and check they're no longer in the tickets group
	_, cathy, _ = testdb.Cathy.Load(rt, oa)
	assert.Equal(t, 1, len(cathy.Groups().All()))
	assert.Equal(t, "Doctors", cathy.Groups().All()[0].Name())

	// but no events for ticket #2 which was already closed
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent WHERE ticket_id = $1 AND event_type = 'C'`, ticket2.ID).Returns(0)

	// can close tickets without a user
	ticket3 := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)
	modelTicket3 := ticket3.Load(rt)

	evts, err = models.CloseTickets(ctx, rt, oa, models.NilUserID, []*models.Ticket{modelTicket3})
	require.NoError(t, err)
	assert.Equal(t, 1, len(evts))
	assert.Equal(t, models.TicketEventTypeClosed, evts[modelTicket3].EventType())

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent WHERE ticket_id = $1 AND event_type = 'C' AND created_by_id IS NULL`, ticket3.ID).Returns(1)
}

func TestReopenTickets(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	ticket1 := testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)
	modelTicket1 := ticket1.Load(rt)

	ticket2 := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)
	modelTicket2 := ticket2.Load(rt)

	evts, err := models.ReopenTickets(ctx, rt, oa, testdb.Admin.ID, []*models.Ticket{modelTicket1, modelTicket2})
	require.NoError(t, err)
	assert.Equal(t, 1, len(evts))
	assert.Equal(t, models.TicketEventTypeReopened, evts[modelTicket1].EventType())

	// check ticket #1 is now closed
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE id = $1 AND status = 'O' AND closed_on IS NULL`, ticket1.ID).Returns(1)

	// and there's reopened event for it
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent WHERE org_id = $1 AND ticket_id = $2 AND event_type = 'R'`, testdb.Org1.ID, ticket1.ID).Returns(1)

	// but no events for ticket #2 which waas already open
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent WHERE ticket_id = $1 AND event_type = 'R'`, ticket2.ID).Returns(0)

	// check Cathy is now in the open tickets group
	_, cathy, _ := testdb.Cathy.Load(rt, oa)
	assert.Equal(t, 2, len(cathy.Groups().All()))
	assert.Equal(t, "Doctors", cathy.Groups().All()[0].Name())
	assert.Equal(t, "Open Tickets", cathy.Groups().All()[1].Name())

	// reopening doesn't change opening daily counts
	testsuite.AssertDailyCounts(t, rt, testdb.Org1, map[string]int{})
}

func TestTicketRecordReply(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	openedOn := time.Date(2022, 5, 17, 14, 21, 0, 0, time.UTC)
	repliedOn := time.Date(2022, 5, 18, 15, 0, 0, 0, time.UTC)

	ticket := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, openedOn, nil)

	err = models.RecordTicketReply(ctx, rt.DB, oa, ticket.ID, testdb.Agent.ID, repliedOn)
	assert.NoError(t, err)

	modelTicket := ticket.Load(rt)
	assert.Equal(t, repliedOn, *modelTicket.RepliedOn())
	assert.Equal(t, repliedOn, modelTicket.LastActivityOn())

	assertdb.Query(t, rt.DB, `SELECT replied_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(repliedOn)
	assertdb.Query(t, rt.DB, `SELECT last_activity_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(repliedOn)

	// check counts were added
	openYmd := openedOn.In(oa.Env().Timezone()).Format("2006-01-02")
	replyYmd := repliedOn.In(oa.Env().Timezone()).Format("2006-01-02")
	testsuite.AssertDailyCounts(t, rt, testdb.Org1, map[string]int{
		replyYmd + "/msgs:ticketreplies:2:6": 1,
		openYmd + "/ticketresptime:total":    88740,
		openYmd + "/ticketresptime:count":    1,
	})
	testsuite.AssertDailyCounts(t, rt, testdb.Org2, map[string]int{})

	repliedAgainOn := time.Date(2022, 5, 18, 15, 5, 0, 0, time.UTC)

	// if we call it again, it won't change replied_on again but it will update last_activity_on
	err = models.RecordTicketReply(ctx, rt.DB, oa, ticket.ID, testdb.Agent.ID, repliedAgainOn)
	assert.NoError(t, err)

	modelTicket = ticket.Load(rt)
	assert.Equal(t, repliedOn, *modelTicket.RepliedOn())
	assert.Equal(t, repliedAgainOn, modelTicket.LastActivityOn())

	assertdb.Query(t, rt.DB, `SELECT replied_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(repliedOn)
	assertdb.Query(t, rt.DB, `SELECT last_activity_on FROM tickets_ticket WHERE id = $1`, ticket.ID).Returns(repliedAgainOn)

	testsuite.AssertDailyCounts(t, rt, testdb.Org1, map[string]int{
		replyYmd + "/msgs:ticketreplies:2:6": 2,
		openYmd + "/ticketresptime:total":    88740,
		openYmd + "/ticketresptime:count":    1,
	})
}
