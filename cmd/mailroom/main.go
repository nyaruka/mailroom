package main

import (
	"github.com/nyaruka/mailroom/v26/cmd"

	_ "github.com/nyaruka/mailroom/v26/core/runner/handlers"
	_ "github.com/nyaruka/mailroom/v26/core/runner/hooks"
	_ "github.com/nyaruka/mailroom/v26/services/airtime/dtone"
	_ "github.com/nyaruka/mailroom/v26/services/ivr/bandwidth"
	_ "github.com/nyaruka/mailroom/v26/services/ivr/twiml"
	_ "github.com/nyaruka/mailroom/v26/services/ivr/vonage"
	_ "github.com/nyaruka/mailroom/v26/services/llm/anthropic"
	_ "github.com/nyaruka/mailroom/v26/services/llm/deepseek"
	_ "github.com/nyaruka/mailroom/v26/services/llm/google"
	_ "github.com/nyaruka/mailroom/v26/services/llm/openai"
	_ "github.com/nyaruka/mailroom/v26/services/llm/openai_azure"
	_ "github.com/nyaruka/mailroom/v26/web/android"
	_ "github.com/nyaruka/mailroom/v26/web/campaign"
	_ "github.com/nyaruka/mailroom/v26/web/channel"
	_ "github.com/nyaruka/mailroom/v26/web/contact"
	_ "github.com/nyaruka/mailroom/v26/web/flow"
	_ "github.com/nyaruka/mailroom/v26/web/llm"
	_ "github.com/nyaruka/mailroom/v26/web/msg"
	_ "github.com/nyaruka/mailroom/v26/web/org"
	_ "github.com/nyaruka/mailroom/v26/web/po"
	_ "github.com/nyaruka/mailroom/v26/web/public"
	_ "github.com/nyaruka/mailroom/v26/web/simulation"
	_ "github.com/nyaruka/mailroom/v26/web/system"
	_ "github.com/nyaruka/mailroom/v26/web/ticket"
)

var (
	// https://goreleaser.com/cookbooks/using-main.version
	version = "dev"
	date    = "unknown"
)

func main() {
	cmd.Run(cmd.Service(version, date))
}
