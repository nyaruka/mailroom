package hooks

import (
	"github.com/nyaruka/mailroom/v26/core/runner"
)

// IndexContacts is our hook for indexing contacts to Elastic after the database transaction has committed
var IndexContacts = runner.IndexContacts
