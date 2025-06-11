package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/nyaruka/mailroom"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"

	_ "github.com/nyaruka/mailroom/services/llm/anthropic"
	_ "github.com/nyaruka/mailroom/services/llm/deepseek"
	_ "github.com/nyaruka/mailroom/services/llm/google"
	_ "github.com/nyaruka/mailroom/services/llm/openai"
	_ "github.com/nyaruka/mailroom/services/llm/openai_azure"
)

// command line tool to run LLM prompt tests against a local test database with real LLMs.
//
// go install github.com/nyaruka/mailroom/cmd/mrllmtests; mrllmtests
func main() {
	ctx := context.TODO()
	config := runtime.LoadConfig()

	slog.SetDefault(slog.New(slog.DiscardHandler)) // disable logging

	mr := mailroom.NewMailroom(config)
	err := mr.Start()
	if err != nil {
		fmt.Printf("unable to start mailroom: %s", err.Error())
		os.Exit(1)
	}

	if err := runPromptTests(ctx, mr.Runtime(), models.OrgID(1)); err != nil {
		fmt.Printf("error running LLM tests: %s", err.Error())
		os.Exit(1)
	}

	mr.Stop()
}
