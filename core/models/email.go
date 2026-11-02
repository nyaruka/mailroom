package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const (
	// maximum number of identical emails an org can send through the default SMTP service in an hour
	emailRepetitionLimit = 20

	// maximum number of recipients an org can send to through the default SMTP service in a day
	emailDailyLimit = 5000
)

var errEmailLimitReached = errors.New("email not sent because org has reached its email sending limit")

// refused emails are kept out of the daily count, so that they can't use up the org's limit and block its other emails
var emailCountsScript = valkey.NewScript(2, `
local repetitions_key, daily_key = KEYS[1], KEYS[2]
local org_id, email_hash, recipients = ARGV[1], ARGV[2], tonumber(ARGV[3])
local repetition_limit, daily_limit = tonumber(ARGV[4]), tonumber(ARGV[5])

local repetitions = redis.call("HINCRBY", repetitions_key, org_id .. "|" .. email_hash, 1)
redis.call("EXPIRE", repetitions_key, 3600)

if repetitions > repetition_limit then
	return {repetitions, 0}
end

local daily = tonumber(redis.call("HGET", daily_key, org_id) or 0) + recipients
if daily > daily_limit then
	return {repetitions, daily}
end

redis.call("HSET", daily_key, org_id, daily)
redis.call("EXPIRE", daily_key, 86400)

return {repetitions, daily}
`)

// GetEmailCounts records an email from the given org and gets the number of times it has been repeated in the current
// hour, and the number of recipients the org has sent to today including this email. Emails over either limit aren't
// added to the latter.
func GetEmailCounts(rp *valkey.Pool, orgID OrgID, addresses []string, subject, body string) (int, int, error) {
	vc := rp.Get()
	defer vc.Close()

	now := dates.Now().UTC()
	repetitionsKey := fmt.Sprintf("email_repetitions:%s", now.Truncate(time.Hour).Format("2006-01-02T15"))
	dailyKey := fmt.Sprintf("email_daily:%s", now.Format("2006-01-02"))

	normalized := strings.Split(strings.ToLower(strings.Join(addresses, "\n")), "\n")
	slices.Sort(normalized)

	hash := sha256.Sum256([]byte(strings.Join(normalized, ",") + "\n" + subject + "\n" + body))

	counts, err := valkey.Ints(emailCountsScript.Do(
		vc, repetitionsKey, dailyKey,
		orgID, hex.EncodeToString(hash[:]), len(addresses), emailRepetitionLimit, emailDailyLimit,
	))
	if err != nil {
		return 0, 0, err
	}
	return counts[0], counts[1], nil
}

// limitedEmailService wraps an email service to stop one org exhausting a service which is shared by all orgs
type limitedEmailService struct {
	rt    *runtime.Runtime
	orgID OrgID
	svc   flows.EmailService
}

func (s *limitedEmailService) Send(ctx context.Context, addresses []string, subject, body string) error {
	repetitions, daily, err := GetEmailCounts(s.rt.VK, s.orgID, addresses, subject, body)
	if err != nil {
		return fmt.Errorf("error looking up email counts: %w", err)
	}
	if repetitions > emailRepetitionLimit || daily > emailDailyLimit {
		slog.Warn("email limit reached, not sending", "org_id", s.orgID, "repetitions", repetitions, "daily", daily)

		return errEmailLimitReached
	}

	return s.svc.Send(ctx, addresses, subject, body)
}
