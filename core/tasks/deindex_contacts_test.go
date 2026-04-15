package tasks_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestDeindexContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetElastic)

	testsuite.IndexContacts(t, rt)

	// insert some messages for Bob and Cat so we can verify message deindexing
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "019b21e1-ba00-7000-8000-000000000001", testdb.TwilioChannel, testdb.Bob, "hello from bob", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "019b21e1-ba00-7000-8000-000000000002", testdb.TwilioChannel, testdb.Cat, "hello from cat", models.MsgStatusHandled, "")

	rt.DB.MustExec(`UPDATE contacts_contact SET last_seen_on = NOW() WHERE id IN ($1, $2)`, testdb.Bob.ID, testdb.Cat.ID)

	testsuite.IndexMessages(t, rt)

	// queue and perform a task to deindex Bob and Cat
	tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, &tasks.DeindexContacts{
		ContactUUIDs: []flows.ContactUUID{testdb.Bob.UUID, testdb.Cat.UUID},
	}, false)

	counts := testsuite.FlushTasks(t, rt)

	assert.Equal(t, map[string]int{"deindex_contacts": 1}, counts)
}
