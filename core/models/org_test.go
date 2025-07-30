package models_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadOrg(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, testsuite.ResetAll)

	tz, _ := time.LoadLocation("America/Los_Angeles")

	rt.DB.MustExec("UPDATE channels_channel SET country = 'FR' WHERE id = $1;", testdb.FacebookChannel.ID)
	rt.DB.MustExec("UPDATE channels_channel SET country = 'US' WHERE id IN ($1,$2);", testdb.TwilioChannel.ID, testdb.VonageChannel.ID)

	rt.DB.MustExec(`UPDATE orgs_org SET flow_languages = '{"fra", "eng"}' WHERE id = $1`, testdb.Org1.ID)
	rt.DB.MustExec(`UPDATE orgs_org SET flow_smtp = 'smtp://foo:bar' WHERE id = $1`, testdb.Org1.ID)
	rt.DB.MustExec(`UPDATE orgs_org SET is_suspended = TRUE WHERE id = $1`, testdb.Org2.ID)
	rt.DB.MustExec(`UPDATE orgs_org SET flow_languages = '{}' WHERE id = $1`, testdb.Org2.ID)
	rt.DB.MustExec(`UPDATE orgs_org SET date_format = 'M' WHERE id = $1`, testdb.Org2.ID)

	org, err := models.LoadOrg(ctx, rt.DB.DB, testdb.Org1.ID)
	assert.NoError(t, err)

	assert.Equal(t, models.OrgID(1), org.ID())
	assert.False(t, org.Suspended())
	assert.Equal(t, "smtp://foo:bar", org.FlowSMTP())
	assert.Equal(t, envs.DateFormatDayMonthYear, org.Environment().DateFormat())
	assert.Equal(t, envs.TimeFormatHourMinute, org.Environment().TimeFormat())
	assert.Equal(t, envs.RedactionPolicyNone, org.Environment().RedactionPolicy())
	assert.Equal(t, "US", string(org.Environment().DefaultCountry()))
	assert.Equal(t, tz, org.Environment().Timezone())
	assert.Equal(t, []i18n.Language{"fra", "eng"}, org.Environment().AllowedLanguages())
	assert.Equal(t, i18n.Language("fra"), org.Environment().DefaultLanguage())
	assert.Equal(t, i18n.Locale("fra-US"), org.Environment().DefaultLocale())

	org, err = models.LoadOrg(ctx, rt.DB.DB, testdb.Org2.ID)
	assert.NoError(t, err)
	assert.True(t, org.Suspended())
	assert.Equal(t, "", org.FlowSMTP())
	assert.Equal(t, envs.DateFormatMonthDayYear, org.Environment().DateFormat())
	assert.Equal(t, []i18n.Language{}, org.Environment().AllowedLanguages())
	assert.Equal(t, i18n.NilLanguage, org.Environment().DefaultLanguage())
	assert.Equal(t, i18n.NilLocale, org.Environment().DefaultLocale())

	_, err = models.LoadOrg(ctx, rt.DB.DB, 99)
	assert.EqualError(t, err, "no org with id: 99")
}

func TestGetOrgIDFromUUID(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, testsuite.ResetAll)

	// mark org 2 deleted
	rt.DB.MustExec(`UPDATE orgs_org SET is_active = FALSE WHERE id = $1`, testdb.Org2.ID)
	models.FlushCache()

	orgID, err := models.GetOrgIDFromUUID(ctx, rt.DB.DB, models.OrgUUID(testdb.Org1.UUID))
	require.NoError(t, err)
	assert.Equal(t, testdb.Org1.ID, orgID)

	orgID2, err := models.GetOrgIDFromUUID(ctx, rt.DB.DB, models.OrgUUID(testdb.Org2.UUID))
	require.NoError(t, err)
	assert.Equal(t, orgID2, models.NilOrgID)

}

func TestEmailService(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, testsuite.ResetAll)

	// make org 2 a child of org 1
	rt.DB.MustExec(`UPDATE orgs_org SET parent_id = $2 WHERE id = $1`, testdb.Org2.ID, testdb.Org1.ID)
	models.FlushCache()

	org1, err := models.LoadOrg(ctx, rt.DB.DB, testdb.Org1.ID)
	require.NoError(t, err)
	org2, err := models.LoadOrg(ctx, rt.DB.DB, testdb.Org2.ID)
	require.NoError(t, err)

	// no SMTP config by default.. no email service
	_, err = org1.EmailService(ctx, rt, nil)
	assert.EqualError(t, err, "missing SMTP configuration")

	rt.Config.SMTPServer = `smtp://foo:bar@example.com?from=foo%40example.com`

	// construct one from the config setting
	svc, err := org1.EmailService(ctx, rt, nil)
	assert.NoError(t, err)
	assert.NotNil(t, svc)

	// set explicitly for org 1
	rt.Config.SMTPServer = ""
	rt.DB.MustExec(`UPDATE orgs_org SET flow_smtp = 'smtp://zed:123@flows.com?from=foo%40flows.com' WHERE id = $1`, testdb.Org1.ID)
	models.FlushCache()

	org1, err = models.LoadOrg(ctx, rt.DB.DB, testdb.Org1.ID)
	require.NoError(t, err)

	svc, err = org1.EmailService(ctx, rt, nil)
	assert.NoError(t, err)
	assert.NotNil(t, svc)

	// org 2 should inherit its from org 1
	svc, err = org2.EmailService(ctx, rt, nil)
	assert.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestStoreAttachment(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, testsuite.ResetStorage)

	image, err := os.Open("testdata/test.jpg")
	require.NoError(t, err)

	org, err := models.LoadOrg(ctx, rt.DB.DB, testdb.Org1.ID)
	assert.NoError(t, err)

	attachment, err := org.StoreAttachment(context.Background(), rt, "668383ba-387c-49bc-b164-1213ac0ea7aa.jpg", "image/jpeg", image)
	require.NoError(t, err)

	assert.Equal(t, utils.Attachment("image/jpeg:http://localhost:9000/test-attachments/attachments/1/6683/83ba/668383ba-387c-49bc-b164-1213ac0ea7aa.jpg"), attachment)

	// err trying to read from same reader again
	_, err = org.StoreAttachment(context.Background(), rt, "668383ba-387c-49bc-b164-1213ac0ea7aa.jpg", "image/jpeg", image)
	assert.EqualError(t, err, "unable to read attachment content: read testdata/test.jpg: file already closed")
}

func TestGetOutboxCounts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, testsuite.ResetData)

	rt.DB.MustExec(`INSERT INTO orgs_itemcount(org_id, scope, count, is_squashed) VALUES ($1, 'msgs:folder:O', -1, FALSE)`, testdb.Org1.ID)
	rt.DB.MustExec(`INSERT INTO orgs_itemcount(org_id, scope, count, is_squashed) VALUES ($1, 'msgs:folder:O', 2, FALSE)`, testdb.Org1.ID)
	rt.DB.MustExec(`INSERT INTO orgs_itemcount(org_id, scope, count, is_squashed) VALUES ($1, 'msgs:folder:O', 3, FALSE)`, testdb.Org1.ID)
	rt.DB.MustExec(`INSERT INTO orgs_itemcount(org_id, scope, count, is_squashed) VALUES ($1, 'msgs:folder:S', 3, FALSE)`, testdb.Org1.ID)
	rt.DB.MustExec(`INSERT INTO orgs_itemcount(org_id, scope, count, is_squashed) VALUES ($1, 'msgs:folder:O', 2, FALSE)`, testdb.Org2.ID)

	counts, err := models.GetOutboxCounts(ctx, rt.DB.DB, []models.OrgID{testdb.Org1.ID, testdb.Org2.ID, 3})
	assert.NoError(t, err)
	assert.Equal(t, map[models.OrgID]int{testdb.Org1.ID: 4, testdb.Org2.ID: 2}, counts)
}
