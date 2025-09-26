package handlers_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestCampaigns(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// insert an event on our campaign that is based on created_on
	testdb.InsertCampaignFlowPoint(t, rt, testdb.RemindersCampaign, testdb.Favorites, testdb.CreatedOnField, 1000, "W")

	// insert an event on our campaign that is based on last_seen_on
	testdb.InsertCampaignFlowPoint(t, rt, testdb.RemindersCampaign, testdb.Favorites, testdb.LastSeenOnField, 2, "D")

	// joined + 1 week => Pick A Number
	// joined + 5 days => Favorites
	// joined + 10 minutes => "Hi @contact.name, it is time to consult with your patients."
	// created_on + 1000 weeks => Favorites
	// last_seen_on + 2 days => Favorites

	runTests(t, rt, "testdata/campaigns.json", false)
}
