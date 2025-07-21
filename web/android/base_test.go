package android_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestEvent(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testsuite.RunWebTests(t, ctx, rt, "testdata/event.json", nil, testsuite.ResetValkey)
}

func TestMessage(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)

	testsuite.RunWebTests(t, ctx, rt, "testdata/message.json", nil, testsuite.ResetValkey)
}

func TestSync(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	androidChannel1 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{})

	testsuite.RunWebTests(t, ctx, rt, "testdata/sync.json", map[string]string{"channel_id_1": fmt.Sprintf("%d", androidChannel1.ID)}, testsuite.ResetNone)
}
