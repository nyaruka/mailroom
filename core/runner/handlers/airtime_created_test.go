package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

var transactionConfirmedResponse = `{
	"creation_date": "2021-03-24T20:05:05.883561000Z",
	"confirmation_date": "2021-03-24T20:05:06.111631000Z",
	"credit_party_identifier": {"mobile_number": "+16055741111"},
	"external_id": "01969b47-47eb-76f8-8e78-3bde7b3370ae",
	"id": 2237512891,
	"status": {
		"class": {"id": 2, "message": "CONFIRMED"},
		"id": 20000,
		"message": "CONFIRMED"
	}
}`

func TestAirtimeCreated(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	rt.Config.DTOneCallbackSecret = "sek"

	defer testsuite.Reset(t, rt, testsuite.ResetAll)
	defer httpx.SetRequestor(httpx.DefaultRequestor)

	// the in-sprint Create resolves the operator + product and submits the unconfirmed transaction; the
	// post-commit hook then POSTs to /confirm with the provider id. HTTP logs surfaced inside the event are
	// test data only and don't make real calls.
	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://dvs-api.dtone.com/v1/async/transactions/2237512891/confirm": {
			httpx.NewMockResponse(200, nil, []byte(transactionConfirmedResponse)),
		},
	}))

	rt.DB.MustExec(`UPDATE orgs_org SET config = '{"dtone_key": "key123", "dtone_secret": "sesame"}'::jsonb WHERE id = $1`, testdb.Org1.ID)

	runTests(t, rt, "testdata/airtime_created.json")
}
