package ctasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestMsgDeleted(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa := testdb.Org1.Load(t, rt)

	ann, _, _ := testdb.Ann.Load(t, rt, oa)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199c4cb-f111-7ce8-9ce9-614d61a2c198", testdb.TwilioChannel, testdb.Ann, "hello", models.MsgStatusHandled)
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199c4cf-486a-79af-9892-79254b6ac5b7", testdb.TwilioChannel, testdb.Ann, "goodbye", models.MsgStatusHandled)

	task := &ctasks.MsgDeletedTask{
		MsgUUID: "0199c4cb-f111-7ce8-9ce9-614d61a2c198",
	}

	err := task.Perform(ctx, rt, oa, ann)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT uuid::text, visibility FROM msgs_msg`).Map(map[string]any{
		"0199c4cb-f111-7ce8-9ce9-614d61a2c198": "X",
		"0199c4cf-486a-79af-9892-79254b6ac5b7": "V",
	})
}
