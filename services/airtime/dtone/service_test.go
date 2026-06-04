package dtone_test

import (
	"context"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/services/airtime/dtone"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

const callbackURL = "https://mailroom.example.com/mr/airtime/dtone/status"

func errorResp(code int, message string) []byte {
	return jsonx.MustMarshal(map[string]any{"errors": []map[string]any{{"code": code, "message": message}}})
}

func TestServiceCreate(t *testing.T) {
	ctx := context.Background()

	reset := test.MockUniverse()
	defer reset()

	client, mocks := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://dvs-api.dtone.com/v1/lookup/mobile-number": {
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)),
			httpx.MockConnectionError,
			httpx.NewMockResponse(200, nil, []byte(`[]`)),
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)),
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)),
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)),
		},
		"https://dvs-api.dtone.com/v1/products?type=FIXED_VALUE_RECHARGE&operator_id=1596&per_page=100": {
			httpx.NewMockResponse(200, nil, []byte(productsResponse)),
			httpx.NewMockResponse(400, nil, errorResp(1003001, "Product is not available in your account")),
			httpx.NewMockResponse(200, nil, []byte(productsResponse)),
			httpx.NewMockResponse(200, nil, []byte(productsResponse)),
		},
		"https://dvs-api.dtone.com/v1/async/transactions": {
			httpx.NewMockResponse(200, nil, []byte(transactionCreatedResponse)),
			httpx.NewMockResponse(400, nil, errorResp(1003001, "Something went wrong")),
		},
	})
	svc := dtone.NewService(client, nil, "key123", "sesame", callbackURL)
	transferUUID := flows.EventUUID("01970fa4-1e58-79d5-bca8-1234567890ab")

	// success — Create resolves the product, submits an unconfirmed transaction, returns currency/amount + external id
	logger := &flows.HTTPLogger{}
	transfer, err := svc.Create(
		ctx,
		transferUUID,
		urns.URN("tel:+593979000000"),
		urns.URN("tel:+593979123456"),
		map[string]decimal.Decimal{
			"USD": decimal.RequireFromString("3"),
			"RWF": decimal.RequireFromString("5000"),
		},
		logger.Log,
	)
	assert.NoError(t, err)
	assert.Equal(t, "2237512891", transfer.ExternalID)
	assert.Equal(t, urns.URN("tel:+593979000000"), transfer.Sender)
	assert.Equal(t, urns.URN("tel:+593979123456"), transfer.Recipient)
	assert.Equal(t, "USD", transfer.Currency)
	assert.Equal(t, decimal.RequireFromString("3"), transfer.Amount)
	assert.Equal(t, 3, len(logger.Logs))

	// lookup connection error
	_, err = svc.Create(ctx, transferUUID, urns.NilURN, urns.URN("tel:+593979123456"), map[string]decimal.Decimal{"USD": decimal.RequireFromString("3")}, logger.Log)
	assert.EqualError(t, err, "number lookup failed: unable to connect to server")

	// lookup returns no operator match
	_, err = svc.Create(ctx, transferUUID, urns.NilURN, urns.URN("tel:+593979123456"), map[string]decimal.Decimal{"USD": decimal.RequireFromString("3")}, logger.Log)
	assert.EqualError(t, err, "unable to find operator for number +593979123456")

	// products fetch fails
	_, err = svc.Create(ctx, transferUUID, urns.NilURN, urns.URN("tel:+593979123456"), map[string]decimal.Decimal{"USD": decimal.RequireFromString("3")}, logger.Log)
	assert.EqualError(t, err, "product fetch failed: Product is not available in your account")

	// no matching product for desired amount
	_, err = svc.Create(ctx, transferUUID, urns.NilURN, urns.URN("tel:+593979123456"), map[string]decimal.Decimal{"USD": decimal.RequireFromString("2")}, logger.Log)
	assert.EqualError(t, err, "unable to find a suitable product for operator 'Claro Ecuador'")

	// transaction submission errors out
	_, err = svc.Create(ctx, transferUUID, urns.NilURN, urns.URN("tel:+593979123456"), map[string]decimal.Decimal{"USD": decimal.RequireFromString("3")}, logger.Log)
	assert.EqualError(t, err, "transaction creation failed: Something went wrong")

	assert.False(t, mocks.HasUnused())
}

func TestServiceConfirm(t *testing.T) {
	ctx := context.Background()

	reset := test.MockUniverse()
	defer reset()

	client, mocks := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://dvs-api.dtone.com/v1/async/transactions/2237512891/confirm": {
			httpx.NewMockResponse(200, nil, []byte(transactionConfirmedResponse)),
			httpx.NewMockResponse(400, nil, errorResp(1003001, "Already confirmed")),
		},
	})
	svc := dtone.NewService(client, nil, "key123", "sesame", callbackURL)

	logger := &flows.HTTPLogger{}
	err := svc.Confirm(ctx, &flows.AirtimeTransfer{ExternalID: "2237512891"}, logger.Log)
	assert.NoError(t, err)

	err = svc.Confirm(ctx, &flows.AirtimeTransfer{ExternalID: "2237512891"}, logger.Log)
	assert.EqualError(t, err, "transaction confirmation failed: Already confirmed")

	err = svc.Confirm(ctx, &flows.AirtimeTransfer{ExternalID: "not-an-int"}, logger.Log)
	assert.ErrorContains(t, err, `invalid transaction id "not-an-int"`)

	assert.False(t, mocks.HasUnused())
}
