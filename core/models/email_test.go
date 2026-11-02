package models_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/smtpx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEmailCounts(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer dates.SetNowFunc(time.Now)
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2026, 11, 1, 12, 13, 3, 0, time.UTC)))

	assertCounts := func(orgID models.OrgID, addresses []string, subject, body string, expRepetitions, expDaily int) {
		repetitions, daily, err := models.GetEmailCounts(rt.VK, orgID, addresses, subject, body)
		require.NoError(t, err)
		assert.Equal(t, expRepetitions, repetitions)
		assert.Equal(t, expDaily, daily)
	}

	one := []string{"bob@nyaruka.com"}
	two := []string{"bob@nyaruka.com", "jim@nyaruka.com"}

	assertCounts(testdb.Org1.ID, two, "Hi", "Hello", 1, 2)
	assertCounts(testdb.Org1.ID, two, "Hi", "Hello", 2, 4)

	// changing any part of the email makes it a different email
	assertCounts(testdb.Org1.ID, one, "Hi", "Hello", 1, 5)
	assertCounts(testdb.Org1.ID, two, "Hey", "Hello", 1, 7)
	assertCounts(testdb.Org1.ID, two, "Hi", "Goodbye", 1, 9)

	// other orgs are counted separately
	assertCounts(testdb.Org2.ID, two, "Hi", "Hello", 1, 2)

	// repetitions are counted per hour, and daily counts carry over
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2026, 11, 1, 13, 1, 0, 0, time.UTC)))
	assertCounts(testdb.Org1.ID, two, "Hi", "Hello", 1, 11)

	// and daily counts are per day
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2026, 11, 2, 0, 1, 0, 0, time.UTC)))
	assertCounts(testdb.Org1.ID, two, "Hi", "Hello", 1, 2)
}

func TestLimitedEmailService(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer smtpx.SetSender(smtpx.DefaultSender)
	sender := smtpx.NewMockSender(make([]error, 2520)...)
	smtpx.SetSender(sender)

	rt.Config.SMTPServer = `smtp://foo:bar@example.com?from=foo%40example.com`

	org, err := models.LoadOrg(ctx, rt.Config, rt.DB.DB, testdb.Org1.ID)
	require.NoError(t, err)

	svc, err := org.EmailService(ctx, rt, nil)
	require.NoError(t, err)

	addresses := []string{"bob@nyaruka.com", "jim@nyaruka.com"}
	limitReached := "email not sent because org has reached its email sending limit"

	// identical emails are sent up to the repetition limit
	for range 20 {
		assert.NoError(t, svc.Send(ctx, addresses, "Hi", "Hello"))
	}
	assert.Len(t, sender.Logs(), 20)

	// and then refused
	assert.EqualError(t, svc.Send(ctx, addresses, "Hi", "Hello"), limitReached)
	assert.Len(t, sender.Logs(), 20)

	// other emails aren't affected, and the refused ones don't count toward the daily limit
	repetitions, daily, err := models.GetEmailCounts(rt.VK, testdb.Org1.ID, addresses, "Other", "Email")
	assert.NoError(t, err)
	assert.Equal(t, 1, repetitions)
	assert.Equal(t, 42, daily)

	// distinct emails are sent up to the daily limit
	for i := range 2479 {
		assert.NoError(t, svc.Send(ctx, addresses, "Hi", fmt.Sprintf("Hello %d", i)))
	}
	assert.Len(t, sender.Logs(), 2499)

	assert.EqualError(t, svc.Send(ctx, addresses, "Hi", "Hello again"), limitReached)
	assert.Len(t, sender.Logs(), 2499)

	// an org with its own SMTP server isn't limited
	rt.DB.MustExec(`UPDATE orgs_org SET flow_smtp = 'smtp://zed:123@flows.com?from=foo%40flows.com' WHERE id = $1`, testdb.Org1.ID)
	models.FlushCache()

	org, err = models.LoadOrg(ctx, rt.Config, rt.DB.DB, testdb.Org1.ID)
	require.NoError(t, err)

	svc, err = org.EmailService(ctx, rt, nil)
	require.NoError(t, err)

	for range 21 {
		assert.NoError(t, svc.Send(ctx, addresses, "Hi", "Hello"))
	}
	assert.Len(t, sender.Logs(), 2520)
}
