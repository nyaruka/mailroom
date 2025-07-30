package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTicketEvents(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(t, testsuite.ResetData)

	ticket := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)
	modelTicket := ticket.Load(rt)

	e1 := models.NewTicketOpenedEvent(modelTicket, testdb.Admin.ID, testdb.Agent.ID, "this is a note")
	assert.Equal(t, testdb.Org1.ID, e1.OrgID())
	assert.Equal(t, testdb.Cathy.ID, e1.ContactID())
	assert.Equal(t, ticket.ID, e1.TicketID())
	assert.Equal(t, models.TicketEventTypeOpened, e1.EventType())
	assert.Equal(t, null.String("this is a note"), e1.Note())
	assert.Equal(t, testdb.Admin.ID, e1.CreatedByID())

	e2 := models.NewTicketAssignedEvent(modelTicket, testdb.Admin.ID, testdb.Agent.ID)
	assert.Equal(t, models.TicketEventTypeAssigned, e2.EventType())
	assert.Equal(t, testdb.Agent.ID, e2.AssigneeID())
	assert.Equal(t, testdb.Admin.ID, e2.CreatedByID())

	e3 := models.NewTicketNoteAddedEvent(modelTicket, testdb.Agent.ID, "please handle")
	assert.Equal(t, models.TicketEventTypeNoteAdded, e3.EventType())
	assert.Equal(t, null.String("please handle"), e3.Note())
	assert.Equal(t, testdb.Agent.ID, e3.CreatedByID())

	e4 := models.NewTicketClosedEvent(modelTicket, testdb.Agent.ID)
	assert.Equal(t, models.TicketEventTypeClosed, e4.EventType())
	assert.Equal(t, testdb.Agent.ID, e4.CreatedByID())

	e5 := models.NewTicketReopenedEvent(modelTicket, testdb.Editor.ID)
	assert.Equal(t, models.TicketEventTypeReopened, e5.EventType())
	assert.Equal(t, testdb.Editor.ID, e5.CreatedByID())

	e6 := models.NewTicketTopicChangedEvent(modelTicket, testdb.Agent.ID, testdb.SupportTopic.ID)
	assert.Equal(t, models.TicketEventTypeTopicChanged, e6.EventType())
	assert.Equal(t, testdb.SupportTopic.ID, e6.TopicID())
	assert.Equal(t, testdb.Agent.ID, e6.CreatedByID())

	err := models.InsertTicketEvents(ctx, rt.DB, []*models.TicketEvent{e1, e2, e3, e4, e5})
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticketevent`).Returns(5)
	assertdb.Query(t, rt.DB, `SELECT assignee_id FROM tickets_ticketevent WHERE id = $1`, e2.ID()).Columns(map[string]any{"assignee_id": int64(testdb.Agent.ID)})
}
