package search_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetElastic|testsuite.ResetDynamo)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "019b21e1-ba00-7000-8000-000000000001", testdb.TwilioChannel, testdb.Ann, "hello world", models.MsgStatusHandled, "")

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "019b2218-a880-7000-8000-000000000002", testdb.TwilioChannel, testdb.Bob, "hello there friend", models.MsgStatusHandled, "019b2218-a880-7000-8000-000000000099")

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "019b224f-9700-7000-8000-000000000003", testdb.TwilioChannel, testdb.Cat, "goodbye world", models.MsgStatusHandled, "")

	testdb.InsertIncomingMsg(t, rt, testdb.Org2, "019b21e1-ba00-7000-8000-000000000004", testdb.Org2Channel, testdb.Org2Contact, "hello world", models.MsgStatusHandled, "")

	rt.DB.MustExec(`UPDATE contacts_contact SET last_seen_on = NOW() WHERE id IN ($1, $2, $3, $4)`, testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID, testdb.Org2Contact.ID)

	testsuite.IndexMessages(t, rt)
	testsuite.WriteMessageHistory(t, rt)

	tcs := []struct {
		label       string
		text        string
		contactUUID flows.ContactUUID
		inTicket    bool
		limit       int
		expected    []flows.ContactUUID
	}{
		{
			label:    "matching two messages",
			text:     "hello",
			limit:    50,
			expected: []flows.ContactUUID{testdb.Bob.UUID, testdb.Ann.UUID},
		},
		{
			label:    "matching one message",
			text:     "goodbye",
			limit:    50,
			expected: []flows.ContactUUID{testdb.Cat.UUID},
		},
		{
			label:    "matching no messages",
			text:     "xyznotfound",
			limit:    50,
			expected: []flows.ContactUUID{},
		},
		{
			label:       "filtered by contact",
			text:        "hello",
			contactUUID: testdb.Bob.UUID,
			limit:       50,
			expected:    []flows.ContactUUID{testdb.Bob.UUID},
		},
		{
			label:    "filtered by in_ticket",
			text:     "hello",
			inTicket: true,
			limit:    50,
			expected: []flows.ContactUUID{testdb.Bob.UUID},
		},
		{
			label:    "without in_ticket returns all",
			text:     "hello",
			inTicket: false,
			limit:    50,
			expected: []flows.ContactUUID{testdb.Bob.UUID, testdb.Ann.UUID},
		},
		{
			label:    "respects limit",
			text:     "hello",
			limit:    1,
			expected: []flows.ContactUUID{testdb.Bob.UUID},
		},
		{
			label:    "multi-word match requires all terms",
			text:     "hello world",
			limit:    50,
			expected: []flows.ContactUUID{testdb.Ann.UUID},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			results, err := search.SearchMessages(ctx, rt, testdb.Org1.ID, tc.text, tc.contactUUID, tc.inTicket, tc.limit)
			require.NoError(t, err)

			contactUUIDs := make([]flows.ContactUUID, len(results))
			for i, r := range results {
				contactUUIDs[i] = r.ContactUUID
			}
			assert.Equal(t, tc.expected, contactUUIDs, "unexpected results for: %s", tc.label)
		})
	}
}
