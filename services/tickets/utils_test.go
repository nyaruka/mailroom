package tickets_test

import (
	"os"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/services/tickets"
	_ "github.com/nyaruka/mailroom/services/tickets/mailgun"
	_ "github.com/nyaruka/mailroom/services/tickets/zendesk"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetContactDisplay(t *testing.T) {
	ctx := testsuite.CTX()
	db := testsuite.DB()

	oa, err := models.GetOrgAssets(ctx, db, testdata.Org1.ID)
	require.NoError(t, err)

	contact, err := models.LoadContact(ctx, db, oa, testdata.Cathy.ID)
	require.NoError(t, err)

	flowContact, err := contact.FlowContact(oa)
	require.NoError(t, err)

	// name if they have one
	assert.Equal(t, "Cathy", tickets.GetContactDisplay(oa.Env(), flowContact))

	flowContact.SetName("")

	// or primary URN
	assert.Equal(t, "(605) 574-1111", tickets.GetContactDisplay(oa.Env(), flowContact))

	// but not if org is anon
	anonEnv := envs.NewBuilder().WithRedactionPolicy(envs.RedactionPolicyURNs).Build()
	assert.Equal(t, "10000", tickets.GetContactDisplay(anonEnv, flowContact))
}

func TestFromTicketUUID(t *testing.T) {
	testsuite.ResetDB()
	ctx := testsuite.CTX()
	db := testsuite.DB()

	ticket1UUID := flows.TicketUUID("f7358870-c3dd-450d-b5ae-db2eb50216ba")
	ticket2UUID := flows.TicketUUID("44b7d9b5-6ddd-4a6a-a1c0-8b70ecd06339")

	// create some tickets
	testdata.InsertOpenTicket(t, db, testdata.Org1, testdata.Cathy, testdata.Mailgun, ticket1UUID, "Need help", "Have you seen my cookies?", "")
	testdata.InsertOpenTicket(t, db, testdata.Org1, testdata.Cathy, testdata.Zendesk, ticket2UUID, "Need help", "Have you seen my shoes?", "")

	// break mailgun configuration
	db.MustExec(`UPDATE tickets_ticketer SET config = '{"foo":"bar"}'::jsonb WHERE id = $1`, testdata.Mailgun.ID)

	models.FlushCache()

	// err if no ticket with UUID
	_, _, _, err := tickets.FromTicketUUID(ctx, db, "33c54d0c-bd49-4edf-87a9-c391a75a630c", "mailgun")
	assert.EqualError(t, err, "error looking up ticket 33c54d0c-bd49-4edf-87a9-c391a75a630c")

	// err if no ticketer type doesn't match
	_, _, _, err = tickets.FromTicketUUID(ctx, db, ticket1UUID, "zendesk")
	assert.EqualError(t, err, "error looking up ticketer #1")

	// err if ticketer isn't configured correctly and can't be loaded as a service
	_, _, _, err = tickets.FromTicketUUID(ctx, db, ticket1UUID, "mailgun")
	assert.EqualError(t, err, "error loading ticketer service: missing domain or api_key or to_address or url_base in mailgun config")

	// if all is correct, returns the ticket, ticketer asset, and ticket service
	ticket, ticketer, svc, err := tickets.FromTicketUUID(ctx, db, ticket2UUID, "zendesk")

	assert.NoError(t, err)
	assert.Equal(t, ticket2UUID, ticket.UUID())
	assert.Equal(t, testdata.Zendesk.UUID, ticketer.UUID())
	assert.Implements(t, (*models.TicketService)(nil), svc)

	testsuite.ResetDB()
	models.FlushCache()
}

func TestFromTicketerUUID(t *testing.T) {
	testsuite.ResetDB()
	ctx := testsuite.CTX()
	db := testsuite.DB()

	// break mailgun configuration
	db.MustExec(`UPDATE tickets_ticketer SET config = '{"foo":"bar"}'::jsonb WHERE id = $1`, testdata.Mailgun.ID)

	// err if no ticketer with UUID
	_, _, err := tickets.FromTicketerUUID(ctx, db, "33c54d0c-bd49-4edf-87a9-c391a75a630c", "mailgun")
	assert.EqualError(t, err, "error looking up ticketer 33c54d0c-bd49-4edf-87a9-c391a75a630c")

	// err if no ticketer type doesn't match
	_, _, err = tickets.FromTicketerUUID(ctx, db, testdata.Mailgun.UUID, "zendesk")
	assert.EqualError(t, err, "error looking up ticketer f9c9447f-a291-4f3c-8c79-c089bbd4e713")

	// err if ticketer isn't configured correctly and can't be loaded as a service
	_, _, err = tickets.FromTicketerUUID(ctx, db, testdata.Mailgun.UUID, "mailgun")
	assert.EqualError(t, err, "error loading ticketer service: missing domain or api_key or to_address or url_base in mailgun config")

	// if all is correct, returns the ticketer asset and ticket service
	ticketer, svc, err := tickets.FromTicketerUUID(ctx, db, testdata.Zendesk.UUID, "zendesk")

	assert.NoError(t, err)
	assert.Equal(t, testdata.Zendesk.UUID, ticketer.UUID())
	assert.Implements(t, (*models.TicketService)(nil), svc)

	testsuite.ResetDB()
	models.FlushCache()
}

func TestSendReply(t *testing.T) {
	testsuite.ResetDB()
	ctx := testsuite.CTX()
	rt := testsuite.RT()
	db := rt.DB
	defer testsuite.ResetStorage()

	defer uuids.SetGenerator(uuids.DefaultGenerator)
	uuids.SetGenerator(uuids.NewSeededGenerator(12345))

	imageBody, err := os.Open("../../core/models/testdata/test.jpg")
	require.NoError(t, err)

	image := &tickets.File{URL: "http://coolfiles.com/a.jpg", ContentType: "image/jpeg", Body: imageBody}

	ticketUUID := flows.TicketUUID("f7358870-c3dd-450d-b5ae-db2eb50216ba")

	// create a ticket
	testdata.InsertOpenTicket(t, db, testdata.Org1, testdata.Cathy, testdata.Mailgun, ticketUUID, "Need help", "Have you seen my cookies?", "")

	ticket, err := models.LookupTicketByUUID(ctx, db, ticketUUID)
	require.NoError(t, err)

	msg, err := tickets.SendReply(ctx, rt, ticket, "I'll get back to you", []*tickets.File{image})
	require.NoError(t, err)

	assert.Equal(t, "I'll get back to you", msg.Text())
	assert.Equal(t, testdata.Cathy.ID, msg.ContactID())
	assert.Equal(t, []utils.Attachment{"image/jpeg:https:///_test_media_storage/media/1/1ae9/6956/1ae96956-4b34-433e-8d1a-f05fe6923d6d.jpg"}, msg.Attachments())
	assert.FileExists(t, "_test_media_storage/media/1/1ae9/6956/1ae96956-4b34-433e-8d1a-f05fe6923d6d.jpg")

	// try with file that can't be read (i.e. same file again which is already closed)
	_, err = tickets.SendReply(ctx, rt, ticket, "I'll get back to you", []*tickets.File{image})
	assert.EqualError(t, err, "error storing attachment http://coolfiles.com/a.jpg for ticket reply: unable to read attachment content: read ../../core/models/testdata/test.jpg: file already closed")
}

func TestCloseTicket(t *testing.T) {
	testsuite.Reset()
	ctx := testsuite.CTX()
	rt := testsuite.RT()
	db := rt.DB

	defer dates.SetNowSource(dates.DefaultNowSource)
	defer httpx.SetRequestor(httpx.DefaultRequestor)

	dates.SetNowSource(dates.NewSequentialNowSource(time.Date(2021, 6, 8, 16, 40, 30, 0, time.UTC)))

	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]httpx.MockResponse{
		"https://api.mailgun.net/v3/tickets.rapidpro.io/messages": {
			httpx.NewMockResponse(200, nil, `{
				"id": "<20200426161758.1.590432020254B2BF@tickets.rapidpro.io>",
				"message": "Queued. Thank you."
			}`),
		},
	}))

	// create an open ticket
	ticket1 := models.NewTicket(
		"2ef57efc-d85f-4291-b330-e4afe68af5fe",
		testdata.Org1.ID,
		testdata.Cathy.ID,
		testdata.Mailgun.ID,
		"EX12345",
		"New Ticket",
		"Where are my cookies?",
		map[string]interface{}{
			"contact-display": "Cathy",
		},
	)
	err := models.InsertTickets(ctx, db, []*models.Ticket{ticket1})
	require.NoError(t, err)

	// create a close ticket trigger
	testdata.InsertTicketClosedTrigger(t, db, testdata.Org1, testdata.Favorites)

	oa, err := models.GetOrgAssets(ctx, db, testdata.Org1.ID)
	require.NoError(t, err)

	logger := &models.HTTPLogger{}

	err = tickets.CloseTicket(ctx, rt, oa, ticket1, true, logger)
	require.NoError(t, err)

	testsuite.AssertContactTasks(t, 1, testdata.Cathy.ID, []string{`{"type":"ticket_closed","org_id":1,"task":{"id":1,"org_id":1,"ticket_id":1,"event_type":"C","created_on":"2021-06-08T16:40:31Z"},"queued_on":"2021-06-08T16:40:34Z"}`})
}
