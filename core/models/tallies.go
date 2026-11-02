package models

import (
	"time"

	"github.com/nyaruka/mailroom/v26/utils/tallies"
)

const (
	// TallyEmailsShared is the daily number of emails sent using the shared email configuration, counting each
	// recipient of a send
	TallyEmailsShared = "emails/shared"
)

// TallyRetentions are how long daily tallies are kept
var TallyRetentions = []tallies.Retention{
	{Prefix: TallyEmailsShared + "/", Period: 365 * 24 * time.Hour},
}
