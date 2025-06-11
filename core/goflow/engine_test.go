package goflow_test

import (
	"net/http"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngineWebhook(t *testing.T) {
	_, rt := testsuite.Runtime()

	svc, err := goflow.Engine(rt).Services().Webhook(nil)
	assert.NoError(t, err)

	defer httpx.SetRequestor(httpx.DefaultRequestor)
	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"http://rapidpro.io": {httpx.NewMockResponse(200, nil, []byte("OK"))},
	}))

	request, err := http.NewRequest("GET", "http://rapidpro.io", nil)
	require.NoError(t, err)

	call, err := svc.Call(request)
	assert.NoError(t, err)
	assert.NotNil(t, call)
	assert.Equal(t, "GET / HTTP/1.1\r\nHost: rapidpro.io\r\nUser-Agent: RapidProMailroom/Dev\r\nX-Mailroom-Mode: normal\r\nAccept-Encoding: gzip\r\n\r\n", string(call.RequestTrace))
	assert.Equal(t, "HTTP/1.0 200 OK\r\nContent-Length: 2\r\n\r\n", string(call.ResponseTrace))
	assert.Equal(t, "OK", string(call.ResponseBody))
}

func TestSimulatorAirtime(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	svc, err := goflow.Simulator(ctx, rt).Services().Airtime(nil)
	assert.NoError(t, err)

	amounts := map[string]decimal.Decimal{"USD": decimal.RequireFromString(`1.50`)}

	transfer, err := svc.Transfer(ctx, urns.URN("tel:+593979111111"), urns.URN("tel:+593979222222"), amounts, nil)
	assert.NoError(t, err)

	assert.Equal(t, &flows.AirtimeTransfer{
		Sender:    urns.URN("tel:+593979111111"),
		Recipient: urns.URN("tel:+593979222222"),
		Currency:  "USD",
		Amount:    decimal.RequireFromString(`1.50`),
	}, transfer)
}

func TestSimulatorWebhook(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	svc, err := goflow.Simulator(ctx, rt).Services().Webhook(nil)
	assert.NoError(t, err)

	defer httpx.SetRequestor(httpx.DefaultRequestor)
	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"http://rapidpro.io": {httpx.NewMockResponse(200, nil, []byte("OK"))},
	}))

	request, err := http.NewRequest("GET", "http://rapidpro.io", nil)
	require.NoError(t, err)

	call, err := svc.Call(request)
	assert.NoError(t, err)
	assert.NotNil(t, call)
	assert.Equal(t, "GET / HTTP/1.1\r\nHost: rapidpro.io\r\nUser-Agent: RapidProMailroom/Dev\r\nX-Mailroom-Mode: simulation\r\nAccept-Encoding: gzip\r\n\r\n", string(call.RequestTrace))
	assert.Equal(t, "HTTP/1.0 200 OK\r\nContent-Length: 2\r\n\r\n", string(call.ResponseTrace))
	assert.Equal(t, "OK", string(call.ResponseBody))
}
