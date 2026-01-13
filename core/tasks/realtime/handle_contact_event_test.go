package realtime_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/assert"
)

func TestHandleContactEvent(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	testsuite.QueueRealtimeTask(t, rt, testdb.Org1, testdb.Ann, &ctasks.EventReceivedTask{
		EventUUID:  models.ChannelEventUUID(uuids.NewV7()),
		EventType:  models.EventTypeNewConversation,
		ChannelID:  testdb.FacebookChannel.ID,
		URNID:      testdb.Ann.URNID,
		Extra:      null.Map[any]{},
		NewContact: false,
	})
	testsuite.QueueRealtimeTask(t, rt, testdb.Org1, testdb.Ann, &ctasks.EventReceivedTask{
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
