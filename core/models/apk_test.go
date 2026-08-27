package models_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
)

func TestGetLatestAndroidAppVersion(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	models.FlushLatestAndroidAppVersion()
	t.Cleanup(models.FlushLatestAndroidAppVersion)

	// no APKs uploaded means we have nothing to compare a device's version against
	version, err := models.GetLatestAndroidAppVersion(ctx, rt.DB)
	assert.NoError(t, err)
	assert.Equal(t, "", version)

	rt.DB.MustExec(`INSERT INTO apks_apk(apk_type, apk_file, version, description, created_on) VALUES('R', 'apks/r1.apk', '1.0.0', '', NOW() - INTERVAL '2 days')`)
	rt.DB.MustExec(`INSERT INTO apks_apk(apk_type, apk_file, version, description, created_on) VALUES('R', 'apks/r2.apk', '2.0.0', '', NOW() - INTERVAL '1 day')`)
	rt.DB.MustExec(`INSERT INTO apks_apk(apk_type, apk_file, version, description, created_on) VALUES('M', 'apks/m1.apk', '9.9.9', '', NOW())`)

	// the previous answer is cached, so we don't see those until it's flushed
	version, err = models.GetLatestAndroidAppVersion(ctx, rt.DB)
	assert.NoError(t, err)
	assert.Equal(t, "", version)

	models.FlushLatestAndroidAppVersion()

	// the most recently uploaded relayer APK wins, and message packs are ignored
	version, err = models.GetLatestAndroidAppVersion(ctx, rt.DB)
	assert.NoError(t, err)
	assert.Equal(t, "2.0.0", version)
}
