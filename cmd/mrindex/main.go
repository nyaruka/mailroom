package main

import (
	"log/slog"
	"os"

	"github.com/nyaruka/mailroom/cmd"
)

func main() {
	if err := cmd.Index(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
