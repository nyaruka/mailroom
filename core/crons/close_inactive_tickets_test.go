package crons_test

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/v26/core/crons"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestCloseInactiveTickets(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	longAgo := time.Now().Add(-30 * 24 * time.Hour)
	recently := time.Now().Add(-1 * time.Hour)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, longAgo, nil)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, longAgo.Add(2*time.Minute), nil)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Bob, testdb.DefaultTopic, longAgo.Add(time.Minute), nil)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Cat, testdb.DefaultTopic, recently, nil)

	cron := &crons.CloseInactiveTicketsCron{MaxTickets: 2, TaskBatchSize: 1}

	// disabled by default
	res, err := cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"tickets": 0}, res)
	assert.Equal(t, map[string][]string{}, testsuite.GetQueuedTaskTypes(t, rt))

	rt.Config.TicketMaxIdleDays = 14

	// only the oldest two tickets are handled in a run, Ann's first and Bob's
	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"tickets": 2}, res)
	assert.Equal(t, map[string][]string{
		"batch/1": {"close_inactive_tickets", "close_inactive_tickets"},
	}, testsuite.GetQueuedTaskTypes(t, rt))

	// contacts with several inactive tickets are only queued once
	cron.MaxTickets = 10
	testsuite.ClearTasks(t, rt)

	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"tickets": 3}, res)
	assert.Equal(t, map[string][]string{
		"batch/1": {"close_inactive_tickets", "close_inactive_tickets"},
	}, testsuite.GetQueuedTaskTypes(t, rt))
}
