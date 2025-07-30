package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"

	"github.com/stretchr/testify/assert"
)

func TestHTTPLogs(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer func() { rt.DB.MustExec(`DELETE FROM request_logs_httplog`) }()

	// insert an airtime log
	log := models.NewAirtimeTransferredLog(testdb.Org1.ID, "http://foo.bar", 200, "GET /", "STATUS 200", false, time.Second, 0, time.Now())
	err := models.InsertHTTPLogs(ctx, rt.DB, []*models.HTTPLog{log})
	assert.Nil(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) from request_logs_httplog WHERE org_id = $1 AND status_code = 200 AND is_error = FALSE`, testdb.Org1.ID).Returns(1)

	// insert a log with nil response
	log = models.NewAirtimeTransferredLog(testdb.Org1.ID, "http://foo.bar", 0, "GET /", "", true, time.Second, 0, time.Now())
	err = models.InsertHTTPLogs(ctx, rt.DB, []*models.HTTPLog{log})
	assert.Nil(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) from request_logs_httplog WHERE org_id = $1 AND status_code = 0 AND is_error = TRUE AND response IS NULL`, testdb.Org1.ID).Returns(1)

	// insert a webhook log
	log = models.NewWebhookCalledLog(testdb.Org1.ID, testdb.Favorites.ID, "http://foo.bar", 400, "GET /", "HTTP 200", false, time.Second, 2, time.Now())
	err = models.InsertHTTPLogs(ctx, rt.DB, []*models.HTTPLog{log})
	assert.Nil(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) from request_logs_httplog WHERE org_id = $1 AND status_code = 400 AND flow_id = $2 AND num_retries = 2`, testdb.Org1.ID, testdb.Favorites.ID).Returns(1)
}
