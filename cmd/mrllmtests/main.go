package main

import (
	"log/slog"
	"os"

	"github.com/nyaruka/mailroom/cmd"

	_ "github.com/nyaruka/mailroom/services/llm/anthropic"
	_ "github.com/nyaruka/mailroom/services/llm/deepseek"
	_ "github.com/nyaruka/mailroom/services/llm/google"
	_ "github.com/nyaruka/mailroom/services/llm/openai"
	_ "github.com/nyaruka/mailroom/services/llm/openai_azure"
)

func main() {
	if err := cmd.LLMTests(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
