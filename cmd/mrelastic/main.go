package main

import (
	"github.com/nyaruka/mailroom/v26/cmd"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func main() {
	cmd.Run(cmd.Elastic(runtime.NewDefaultConfig()))
}
