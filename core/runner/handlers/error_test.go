package handlers_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestError(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	mc, contact, _ := testdb.Ann.Load(t, rt, oa)

	// capture logging
	logBuf := &bytes.Buffer{}
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(prevLogger)

	// an ordinary expression error shouldn't be logged as an error
	scene := runner.NewScene(mc, contact)
	err = scene.AddEvent(ctx, rt, oa, events.NewError("error evaluating @(1 / 0)", events.ErrorCodeExpression, "expression", "1 / 0"), models.NilUserID, "")
	require.NoError(t, err)

	assert.NotContains(t, logBuf.String(), "level=ERROR")

	// but an expression too complex error should be
	logBuf.Reset()
	err = scene.AddEvent(ctx, rt, oa, events.NewError("error evaluating @(repeat(...))", events.ErrorCodeExpressionTooComplex, "expression", "repeat(...)"), models.NilUserID, "")
	require.NoError(t, err)

	assert.Contains(t, logBuf.String(), "level=ERROR")
	assert.Contains(t, logBuf.String(), "expression exceeded cost budget")
	assert.Contains(t, logBuf.String(), "expression=repeat(...)")
	assert.Contains(t, logBuf.String(), fmt.Sprintf("org=%d", oa.OrgID()))
	assert.Contains(t, logBuf.String(), "contact="+string(scene.ContactUUID()))

	// as should a webhook request size error
	logBuf.Reset()
	err = scene.AddEvent(ctx, rt, oa, events.NewError("Webhook request evaluated to 300000 bytes, exceeding the limit of 262144", events.ErrorCodeWebhookRequestSize), models.NilUserID, "")
	require.NoError(t, err)

	assert.Contains(t, logBuf.String(), "level=ERROR")
	assert.Contains(t, logBuf.String(), "webhook size limit exceeded")
	assert.Contains(t, logBuf.String(), "code=webhook:request_size")

	// and a webhook response size error
	logBuf.Reset()
	err = scene.AddEvent(ctx, rt, oa, events.NewError("webhook response body exceeds 10000000 bytes limit", events.ErrorCodeWebhookResponseSize), models.NilUserID, "")
	require.NoError(t, err)

	assert.Contains(t, logBuf.String(), "level=ERROR")
	assert.Contains(t, logBuf.String(), "webhook size limit exceeded")
	assert.Contains(t, logBuf.String(), "code=webhook:response_size")
}
