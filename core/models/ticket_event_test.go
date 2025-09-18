package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTicketEvents(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	ticket := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), nil)
	modelTicket := ticket.Load(t, rt, testdb.Org1)

	e1 := models.NewTicketOpenedEvent("019906ed-d7a3-732e-9429-6c99b391c825", modelTicket, testdb.Admin.ID, testdb.Agent.ID, "this is a note")
	assert.Equal(t, flows.EventUUID("019906ed-d7a3-732e-9429-6c99b391c825"), e1.UUID)
	assert.Equal(t, testdb.Org1.ID, e1.OrgID)
	assert.Equal(t, testdb.Ann.ID, e1.ContactID)
	assert.Equal(t, ticket.ID, e1.TicketID)
	assert.Equal(t, models.TicketEventTypeOpened, e1.Type)
	assert.Equal(t, null.String("this is a note"), e1.Note)
	assert.Equal(t, testdb.Admin.ID, e1.CreatedByID)

	e2 := models.NewTicketAssignedEvent("019906ed-d7a3-7977-84d4-765d0f30fc77", modelTicket, testdb.Admin.ID, testdb.Agent.ID)
	assert.Equal(t, flows.EventUUID("019906ed-d7a3-7977-84d4-765d0f30fc77"), e2.UUID)
	assert.Equal(t, models.TicketEventTypeAssigned, e2.Type)
	assert.Equal(t, testdb.Agent.ID, e2.AssigneeID)
	assert.Equal(t, testdb.Admin.ID, e2.CreatedByID)

	e3 := models.NewTicketNoteAddedEvent("019906ed-d7a3-792e-b19e-4fe4d4d2bad8", modelTicket, testdb.Agent.ID, "please handle")
	assert.Equal(t, flows.EventUUID("019906ed-d7a3-792e-b19e-4fe4d4d2bad8"), e3.UUID)
	assert.Equal(t, models.TicketEventTypeNoteAdded, e3.Type)
	assert.Equal(t, null.String("please handle"), e3.Note)
	assert.Equal(t, testdb.Agent.ID, e3.CreatedByID)

	e4 := models.NewTicketClosedEvent("019906ed-d7a3-774d-94da-dc29245a5543", modelTicket, testdb.Agent.ID)
	assert.Equal(t, flows.EventUUID("019906ed-d7a3-774d-94da-dc29245a5543"), e4.UUID)
	assert.Equal(t, models.TicketEventTypeClosed, e4.Type)
	assert.Equal(t, testdb.Agent.ID, e4.CreatedByID)

	e5 := models.NewTicketReopenedEvent("019906ed-d7a3-752c-8d7e-e4666f6e3d71", modelTicket, testdb.Editor.ID)
	assert.Equal(t, flows.EventUUID("019906ed-d7a3-752c-8d7e-e4666f6e3d71"), e5.UUID)
	assert.Equal(t, models.TicketEventTypeReopened, e5.Type)
	assert.Equal(t, testdb.Editor.ID, e5.CreatedByID)

	e6 := models.NewTicketTopicChangedEvent("019906d9-1483-75a4-8bdb-1eed30036909", modelTicket, testdb.Agent.ID, testdb.SupportTopic.ID)
	assert.Equal(t, flows.EventUUID("019906d9-1483-75a4-8bdb-1eed30036909"), e6.UUID)
	assert.Equal(t, models.TicketEventTypeTopicChanged, e6.Type)
	assert.Equal(t, testdb.SupportTopic.ID, e6.TopicID)
	assert.Equal(t, testdb.Agent.ID, e6.CreatedByID)

	err := models.InsertLegacyTicketEvents(ctx, rt.DB, []*models.TicketEvent{e1, e2, e3, e4, e5})
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent`).Returns(5)
	assertdb.Query(t, rt.DB, `SELECT assignee_id FROM tickets_ticketevent WHERE id = $1`, e2.ID).Columns(map[string]any{"assignee_id": testdb.Agent.ID})
}
