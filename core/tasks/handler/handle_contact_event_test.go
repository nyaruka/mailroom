package handler_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks/handler/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/assert"
)

func TestHandleContactEvent(t *testing.T) {
	_, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	testsuite.QueueContactTask(t, rt, testdb.Org1, testdb.Cathy, &ctasks.EventReceivedTask{
		EventType:  models.EventTypeNewConversation,
		ChannelID:  testdb.FacebookChannel.ID,
		URNID:      testdb.Cathy.URNID,
		Extra:      null.Map[any]{},
		CreatedOn:  time.Now(),
		NewContact: false,
	})
	testsuite.QueueContactTask(t, rt, testdb.Org1, testdb.Cathy, &ctasks.EventReceivedTask{
		EventType:  models.EventTypeStopContact,
		ChannelID:  testdb.FacebookChannel.ID,
		URNID:      testdb.Cathy.URNID,
		Extra:      null.Map[any]{},
		CreatedOn:  time.Now(),
		NewContact: false,
	})

	tasksRan := testsuite.FlushTasks(t, rt)
	assert.Equal(t, map[string]int{"handle_contact_event": 2}, tasksRan)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'S'`, testdb.Cathy.ID).Returns(1)
}
