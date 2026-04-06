package main

import (
	"github.com/nyaruka/mailroom/v26/cmd"

	_ "github.com/nyaruka/mailroom/v26/services/llm/anthropic"
	_ "github.com/nyaruka/mailroom/v26/services/llm/deepseek"
	_ "github.com/nyaruka/mailroom/v26/services/llm/google"
	_ "github.com/nyaruka/mailroom/v26/services/llm/openai"
	_ "github.com/nyaruka/mailroom/v26/services/llm/openai_azure"
)

func main() {
	cmd.Run(cmd.LLMTests())
}
