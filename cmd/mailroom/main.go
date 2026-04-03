package main

import (
	"github.com/nyaruka/mailroom/cmd"

	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	_ "github.com/nyaruka/mailroom/core/runner/hooks"
	_ "github.com/nyaruka/mailroom/services/airtime/dtone"
	_ "github.com/nyaruka/mailroom/services/ivr/bandwidth"
	_ "github.com/nyaruka/mailroom/services/ivr/twiml"
	_ "github.com/nyaruka/mailroom/services/ivr/vonage"
	_ "github.com/nyaruka/mailroom/services/llm/anthropic"
	_ "github.com/nyaruka/mailroom/services/llm/deepseek"
	_ "github.com/nyaruka/mailroom/services/llm/google"
	_ "github.com/nyaruka/mailroom/services/llm/openai"
	_ "github.com/nyaruka/mailroom/services/llm/openai_azure"
	_ "github.com/nyaruka/mailroom/web/android"
	_ "github.com/nyaruka/mailroom/web/campaign"
	_ "github.com/nyaruka/mailroom/web/channel"
	_ "github.com/nyaruka/mailroom/web/contact"
	_ "github.com/nyaruka/mailroom/web/flow"
	_ "github.com/nyaruka/mailroom/web/llm"
	_ "github.com/nyaruka/mailroom/web/msg"
	_ "github.com/nyaruka/mailroom/web/org"
	_ "github.com/nyaruka/mailroom/web/po"
	_ "github.com/nyaruka/mailroom/web/public"
	_ "github.com/nyaruka/mailroom/web/simulation"
	_ "github.com/nyaruka/mailroom/web/system"
	_ "github.com/nyaruka/mailroom/web/ticket"
)

var (
	// https://goreleaser.com/cookbooks/using-main.version
	version = "dev"
	date    = "unknown"
)

func main() {
	cmd.Run(cmd.Service(version, date))
}
