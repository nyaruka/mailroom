package templates_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/locks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testChannelType = models.ChannelType("TST")

// a service which returns whatever it's been told to
type testService struct {
	translations []*templates.Translation
	err          error
}

func (s *testService) FetchTemplates(ctx context.Context, clog *models.ChannelLog) ([]*templates.Translation, error) {
	return s.translations, s.err
}

func (s *testService) RedactValues(*models.Channel) []string { return nil }

var svc = &testService{}

func init() {
	templates.RegisterService(testChannelType, func(*runtime.Config, *http.Client, *models.Channel) (templates.Service, error) {
		return svc, nil
	})
}

func newTranslation(name, locale string, numVars int) *templates.Translation {
	vars := make([]*templates.Variable, numVars)
	for i := range vars {
		vars[i] = &templates.Variable{Type: "text"}
	}
	return &templates.Translation{
		Name:           name,
		Locale:         i18n.Locale(locale),
		ExternalLocale: locale,
		Status:         templates.StatusApproved,
		Components:     []*templates.Component{{Name: "body", Type: "body/text", Content: "Hi", Variables: map[string]int{}}},
		Variables:      vars,
		IsSupported:    true,
	}
}

func TestSyncChannel(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	ch := testdb.InsertChannel(t, rt, testdb.Org1, testChannelType, "Test", "1234", []string{"whatsapp"}, "SR", map[string]any{})

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)
	channel := oa.ChannelByID(ch.ID)

	assertTranslations := func(expected string) {
		t.Helper()
		assertdb.Query(t, rt.DB, `SELECT COALESCE(string_agg(t.name || ':' || tr.locale || ':' || tr.is_compatible || ':' || (t.base_translation_id = tr.id), ',' ORDER BY t.name, tr.locale), '')
			FROM templates_templatetranslation tr JOIN templates_template t ON t.id = tr.template_id WHERE tr.channel_id = $1`, ch.ID).Returns(expected)
	}
	resetModifiedOn := func() {
		rt.DB.MustExec(`UPDATE templates_template SET modified_on = '2020-01-01T00:00:00Z'`)
	}
	assertModified := func(expected string) {
		t.Helper()
		assertdb.Query(t, rt.DB, `SELECT COALESCE(string_agg(name, ',' ORDER BY name), '') FROM templates_template WHERE modified_on > '2020-01-01T00:00:00Z'`).Returns(expected)
	}

	// revive_issue already has translations on another channel, the base of which has 2 variables
	svc.translations = []*templates.Translation{
		newTranslation("revive_issue", "eng-US", 2),
		newTranslation("revive_issue", "spa", 1),
		newTranslation("welcome", "fra", 0),
		newTranslation("welcome", "eng", 1),
	}
	resetModifiedOn()

	num, err := templates.SyncChannel(ctx, rt, channel)
	assert.NoError(t, err)
	assert.Equal(t, 4, num)

	// revive_issue keeps its base and our spa translation doesn't match it, and welcome is based on the org's primary
	// language over its oldest
	assertTranslations("revive_issue:eng-US:true:false,revive_issue:spa:false:false,welcome:eng:true:true,welcome:fra:false:false")
	assertModified("revive_issue,welcome")
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM templates_template t JOIN orgs_org o ON o.id = t.org_id
		WHERE t.name = 'welcome' AND NOT t.is_system AND t.created_by_id = o.created_by_id AND t.modified_by_id = o.created_by_id`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT sync_state->'templates'->>'failed' FROM channels_channel WHERE id = $1`, ch.ID).Returns("false")
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM channels_channel WHERE id = $1 AND (sync_state->'templates'->>'synced_on')::timestamptz > NOW() - INTERVAL '1 minute'`, ch.ID).Returns(1)

	// the sync is logged to the channel
	rt.Dynamo.Main.Flush()
	dyntest.AssertCount(t, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), 1)

	// syncing the same again changes nothing
	resetModifiedOn()

	num, err = templates.SyncChannel(ctx, rt, channel)
	assert.NoError(t, err)
	assert.Equal(t, 4, num)
	assertTranslations("revive_issue:eng-US:true:false,revive_issue:spa:false:false,welcome:eng:true:true,welcome:fra:false:false")
	assertModified("")

	// the base of welcome goes and our spa translation is updated to match the base of revive_issue
	svc.translations = []*templates.Translation{
		newTranslation("revive_issue", "eng-US", 2),
		newTranslation("revive_issue", "spa", 2),
		newTranslation("welcome", "fra", 0),
	}
	resetModifiedOn()

	num, err = templates.SyncChannel(ctx, rt, channel)
	assert.NoError(t, err)
	assert.Equal(t, 3, num)
	assertTranslations("revive_issue:eng-US:true:false,revive_issue:spa:true:false,welcome:fra:true:true")
	assertModified("revive_issue,welcome")

	// translations on other channels are untouched
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM templates_templatetranslation WHERE channel_id = $1`, testdb.FacebookChannel.ID).Returns(3)

	// all of welcome's translations go
	svc.translations = []*templates.Translation{newTranslation("revive_issue", "eng-US", 2)}
	resetModifiedOn()

	num, err = templates.SyncChannel(ctx, rt, channel)
	assert.NoError(t, err)
	assert.Equal(t, 1, num)
	assertTranslations("revive_issue:eng-US:true:false")
	assertModified("revive_issue,welcome")
	assertdb.Query(t, rt.DB, `SELECT base_translation_id FROM templates_template WHERE name = 'welcome'`).Returns(nil)

	// a failed fetch leaves existing translations alone and is recorded on the channel
	svc.err = errors.New("boom")
	_, err = templates.SyncChannel(ctx, rt, channel)
	assert.EqualError(t, err, "error fetching templates: boom")

	assertTranslations("revive_issue:eng-US:true:false")
	assertdb.Query(t, rt.DB, `SELECT sync_state->'templates'->>'failed' FROM channels_channel WHERE id = $1`, ch.ID).Returns("true")

	// failures are logged to the channel too
	rt.Dynamo.Main.Flush()
	dyntest.AssertCount(t, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), 5)

	// and the next success clears it
	svc.err = nil
	_, err = templates.SyncChannel(ctx, rt, channel)
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT sync_state->'templates'->>'failed' FROM channels_channel WHERE id = $1`, ch.ID).Returns("false")

	// state of other syncs is left alone
	rt.DB.MustExec(`UPDATE channels_channel SET sync_state = sync_state || '{"android": {"power_level": 50}}' WHERE id = $1`, ch.ID)
	_, err = templates.SyncChannel(ctx, rt, channel)
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT sync_state->'android'->>'power_level' FROM channels_channel WHERE id = $1`, ch.ID).Returns("50")

	// can't sync while another sync, e.g. temba's, holds the lock
	locker := locks.NewLocker(fmt.Sprintf("refresh_channel_templates_%d", ch.ID), time.Minute)
	lock, err := locker.Grab(ctx, rt.VK, 0)
	require.NoError(t, err)

	_, err = templates.SyncChannel(ctx, rt, channel)
	assert.ErrorIs(t, err, templates.ErrSyncInProgress)

	locker.Release(ctx, rt.VK, lock)

	// channel type without a service
	_, err = templates.SyncChannel(ctx, rt, oa.ChannelByID(testdb.TwilioChannel.ID))
	assert.EqualError(t, err, "no registered templates service for channel type: T")
}
