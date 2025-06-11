package dtone_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/services/airtime/dtone"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func errorResp(code int, message string) []byte {
	return jsonx.MustMarshal(map[string]any{"errors": []map[string]any{{"code": code, "message": message}}})
}

func TestServiceWithSuccessfulTranfer(t *testing.T) {
	ctx := context.Background()

	test.MockUniverse()

	mocks := httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://dvs-api.dtone.com/v1/lookup/mobile-number": {
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)), // successful mobile number lookup
		},
		"https://dvs-api.dtone.com/v1/products?type=FIXED_VALUE_RECHARGE&operator_id=1596&per_page=100": {
			httpx.NewMockResponse(200, nil, []byte(productsResponse)),
		},
		"https://dvs-api.dtone.com/v1/async/transactions": {
			httpx.NewMockResponse(200, nil, []byte(transactionConfirmedResponse)),
		},
	})

	defer httpx.SetRequestor(httpx.DefaultRequestor)
	httpx.SetRequestor(mocks)

	svc := dtone.NewService(http.DefaultClient, nil, "key123", "sesame")

	httpLogger := &flows.HTTPLogger{}

	transfer, err := svc.Transfer(
		ctx,
		urns.URN("tel:+593979000000"),
		urns.URN("tel:+593979123456"),
		map[string]decimal.Decimal{
			"USD": decimal.RequireFromString("3"),
			"RWF": decimal.RequireFromString("5000"),
		},
		httpLogger.Log,
	)
	assert.NoError(t, err)
	assert.Equal(t, &flows.AirtimeTransfer{
		UUID:       "01969b47-0583-76f8-ae7f-f8b243c49ff5",
		ExternalID: "2237512891",
		Sender:     urns.URN("tel:+593979000000"),
		Recipient:  urns.URN("tel:+593979123456"),
		Currency:   "USD",
		Amount:     decimal.RequireFromString("3"),
	}, transfer)

	assert.Equal(t, 3, len(httpLogger.Logs))

	assert.False(t, mocks.HasUnused())
}

func TestServiceFailedTransfers(t *testing.T) {
	ctx := context.Background()

	test.MockUniverse()

	mocks := httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://dvs-api.dtone.com/v1/lookup/mobile-number": {
			httpx.MockConnectionError, // timeout
			httpx.NewMockResponse(400, nil, errorResp(1005003, "Credit party mobile number is invalid")),
			httpx.NewMockResponse(200, nil, []byte(`[]`)), // no matches
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)),
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)),
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)),
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)),
			httpx.NewMockResponse(200, nil, []byte(lookupNumberResponse)),
		},
		"https://dvs-api.dtone.com/v1/products?type=FIXED_VALUE_RECHARGE&operator_id=1596&per_page=100": {
			httpx.NewMockResponse(400, nil, errorResp(1003001, "Product is not available in your account")),
			httpx.NewMockResponse(200, nil, []byte(`[]`)), // no products
			httpx.NewMockResponse(200, nil, []byte(productsResponse)),
			httpx.NewMockResponse(200, nil, []byte(productsResponse)),
			httpx.NewMockResponse(200, nil, []byte(productsResponse)),
		},
		"https://dvs-api.dtone.com/v1/async/transactions": {
			httpx.NewMockResponse(400, nil, errorResp(1003001, "Something went wrong")),
			httpx.NewMockResponse(200, nil, []byte(transactionRejectedResponse)),
		},
	})

	defer httpx.SetRequestor(httpx.DefaultRequestor)
	httpx.SetRequestor(mocks)

	svc := dtone.NewService(http.DefaultClient, nil, "key123", "sesame")

	httpLogger := &flows.HTTPLogger{}
	amounts := map[string]decimal.Decimal{"USD": decimal.RequireFromString("3")}

	// try when phone number lookup gives a connection error
	transfer, err := svc.Transfer(ctx, urns.URN("tel:+593979000000"), urns.URN("tel:+593979123456"), amounts, httpLogger.Log)
	assert.EqualError(t, err, "number lookup failed: unable to connect to server")
	assert.Equal(t, urns.URN("tel:+593979000000"), transfer.Sender)
	assert.Equal(t, urns.URN("tel:+593979123456"), transfer.Recipient)
	assert.Equal(t, decimal.Zero, transfer.Amount)

	// try when phone number lookup fails
	transfer, err = svc.Transfer(ctx, urns.URN("tel:+593979000000"), urns.URN("tel:+593979123456"), amounts, httpLogger.Log)
	assert.EqualError(t, err, "number lookup failed: Credit party mobile number is invalid")
	assert.NotNil(t, transfer)

	// try when phone number lookup returns no matches
	transfer, err = svc.Transfer(ctx, urns.URN("tel:+593979000000"), urns.URN("tel:+593979123456"), amounts, httpLogger.Log)
	assert.EqualError(t, err, "unable to find operator for number +593979123456")
	assert.NotNil(t, transfer)

	// try when product fetch fails
	transfer, err = svc.Transfer(ctx, urns.URN("tel:+593979000000"), urns.URN("tel:+593979123456"), amounts, httpLogger.Log)
	assert.EqualError(t, err, "product fetch failed: Product is not available in your account")
	assert.NotNil(t, transfer)

	// try when we can't find any suitable products
	transfer, err = svc.Transfer(ctx, urns.URN("tel:+593979000000"), urns.URN("tel:+593979123456"), amounts, httpLogger.Log)
	assert.EqualError(t, err, "unable to find a suitable product for operator 'Claro Ecuador'")
	assert.NotNil(t, transfer)

	// try when we can't find any suitable products (there are products but none match the amount)
	transfer, err = svc.Transfer(ctx, urns.URN("tel:+593979000000"), urns.URN("tel:+593979123456"), map[string]decimal.Decimal{"USD": decimal.RequireFromString("2")}, httpLogger.Log)
	assert.EqualError(t, err, "unable to find a suitable product for operator 'Claro Ecuador'")
	assert.NotNil(t, transfer)

	// try when transaction request errors
	transfer, err = svc.Transfer(ctx, urns.URN("tel:+593979000000"), urns.URN("tel:+593979123456"), amounts, httpLogger.Log)
	assert.EqualError(t, err, "transaction creation failed: Something went wrong")
	assert.NotNil(t, transfer)

	// try when transaction is rejected
	transfer, err = svc.Transfer(ctx, urns.URN("tel:+593979000000"), urns.URN("tel:+593979123456"), amounts, httpLogger.Log)
	assert.EqualError(t, err, "transaction to send product 6035 on operator 1596 ended with status REJECTED-OPERATOR-CURRENTLY-UNAVAILABLE")
	assert.NotNil(t, transfer)
}
