package android_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestEvent(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)

	testsuite.RunWebTests(t, ctx, rt, "testdata/event.json", nil)

	orgTasks := testsuite.CurrentTasks(t, rt, "handler")[testdb.Org1.ID]
	assert.Len(t, orgTasks, 1)
	assert.Equal(t, "handle_contact_event", orgTasks[0].Type)
}

func TestMessage(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)

	testsuite.RunWebTests(t, ctx, rt, "testdata/message.json", nil)

	orgTasks := testsuite.CurrentTasks(t, rt, "handler")[testdb.Org1.ID]
	assert.Len(t, orgTasks, 2)
	assert.Equal(t, "handle_contact_event", orgTasks[0].Type)
	assert.Equal(t, "handle_contact_event", orgTasks[1].Type)
}

func TestSync(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	androidChannel1 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{})

	testsuite.RunWebTests(t, ctx, rt, "testdata/sync.json", map[string]string{"channel_id_1": fmt.Sprintf("%d", androidChannel1.ID)})
}
