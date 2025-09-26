package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactURNsChanged(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// add a URN to Cat that Ann will steal
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Cat, urns.URN("tel:+12065551212"), 100, nil)

	runTests(t, rt, "testdata/contact_urns_changed.json")
}
