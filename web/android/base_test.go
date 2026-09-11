package android_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestSync(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{})
	testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 2", "234", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID3"})

	testsuite.RunWebTests(t, rt, "testdata/sync.json")
}
