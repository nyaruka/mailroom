package handlers_test

import (
	"testing"
	"time"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
)

func TestTicketAssigneeChanged(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	openedOn := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, openedOn, nil)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Bob, testdb.DefaultTopic, openedOn, testdb.Admin)

	oa := testdb.Org1.Load(t, rt)
	agent := oa.SessionAssets().Users().Get(testdb.Agent.UUID)

	subscribeTickets(t, rt)

	// a first assignment is tallied on the day it's made, but a reassignment isn't
	applyModifiers(t, rt, oa, testdb.Ann, modifiers.NewTicketAssignee("01992f54-5ab6-717a-a39e-e8ca91fb7262", agent))
	applyModifiers(t, rt, oa, testdb.Bob, modifiers.NewTicketAssignee("01992f54-5ab6-725e-be9c-0c6407efd755", agent))

	assert.Equal(t, []core.TicketUUID{"01992f54-5ab6-717a-a39e-e8ca91fb7262", "01992f54-5ab6-725e-be9c-0c6407efd755"}, ticketsChangedSince(t, rt, 0))

	vc := rt.VK.Get()
	defer vc.Close()

	today := time.Now().UTC().Format(time.DateOnly)
	assertvk.HGetAll(t, vc, "{tallies}:live", map[string]string{"O1|tickets/assigned:5@" + today: "1"})
}
