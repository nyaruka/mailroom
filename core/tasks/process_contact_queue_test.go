package tasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/assert"
)

func TestProcessContactQueue(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	vc := rt.VK.Get()
	defer vc.Close()

	testsuite.QueueContactTask(t, rt, testdb.Org1, testdb.Ann, &ctasks.EventReceived{
		EventUUID:  models.ChannelEventUUID(uuids.NewV7()),
		EventType:  models.EventTypeNewConversation,
		ChannelID:  testdb.FacebookChannel.ID,
		URNID:      testdb.Ann.URNID,
		Extra:      null.Map[any]{},
		NewContact: false,
	})
	testsuite.QueueContactTask(t, rt, testdb.Org1, testdb.Ann, &ctasks.EventReceived{
		EventUUID:  models.ChannelEventUUID(uuids.NewV7()),
		EventType:  models.EventTypeStopContact,
		ChannelID:  testdb.FacebookChannel.ID,
		URNID:      testdb.Ann.URNID,
		Extra:      null.Map[any]{},
		NewContact: false,
	})

	tasksRan := testsuite.FlushTasks(t, rt)
	assert.Equal(t, map[string]int{"handle_contact_event": 2}, tasksRan)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'S'`, testdb.Ann.ID).Returns(1)
}
