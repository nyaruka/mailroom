package runtime_test

import (
	"flag"
	"testing"

	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/assert"
)

func TestSplitArgs(t *testing.T) {
	newFlags := func() *flag.FlagSet {
		fs := flag.NewFlagSet("cmd", flag.ContinueOnError)
		fs.String("since", "", "a command flag which takes a value")
		fs.Bool("delete", false, "a command flag which doesn't")
		return fs
	}

	tcs := []struct {
		args       []string
		cmdArgs    []string
		cfgArgs    []string
		positional []string
	}{
		{args: nil},

		{ // positional args only
			args:       []string{"index", "contacts"},
			positional: []string{"index", "contacts"},
		},
		{ // our own bool flag
			args:       []string{"-delete", "prune", "contacts"},
			cmdArgs:    []string{"-delete"},
			positional: []string{"prune", "contacts"},
		},
		{ // our own flag with a value, in both forms
			args:       []string{"--since=yesterday", "index", "messages"},
			cmdArgs:    []string{"--since=yesterday"},
			positional: []string{"index", "messages"},
		},
		{
			args:       []string{"-since", "yesterday", "index", "messages"},
			cmdArgs:    []string{"-since", "yesterday"},
			positional: []string{"index", "messages"},
		},
		{ // config flags are left for the config loader, including their values
			args:       []string{"--db=postgres://temba@localhost/temba", "index", "contacts"},
			cfgArgs:    []string{"--db=postgres://temba@localhost/temba"},
			positional: []string{"index", "contacts"},
		},
		{
			args:       []string{"-db", "postgres://temba@localhost/temba", "index", "contacts"},
			cfgArgs:    []string{"-db", "postgres://temba@localhost/temba"},
			positional: []string{"index", "contacts"},
		},
		{ // a boolean config flag doesn't swallow the argument after it
			args:       []string{"-s3-path-style", "prune", "contacts"},
			cfgArgs:    []string{"-s3-path-style"},
			positional: []string{"prune", "contacts"},
		},
		{ // ours and the config loader's, mixed, before and after the positional args
			args:       []string{"-delete", "-log-level", "debug", "prune", "contacts", "--dynamo-table-prefix=Test"},
			cmdArgs:    []string{"-delete"},
			cfgArgs:    []string{"-log-level", "debug", "--dynamo-table-prefix=Test"},
			positional: []string{"prune", "contacts"},
		},
		{ // unknown flags go to the config loader which will reject them, and don't swallow what follows
			args:       []string{"-not-a-flag", "prune", "contacts"},
			cfgArgs:    []string{"-not-a-flag"},
			positional: []string{"prune", "contacts"},
		},
		{ // -help is for the config loader
			args:    []string{"-help"},
			cfgArgs: []string{"-help"},
		},
		{ // a flag needing a value but not given one is left for the flag set to report
			args:    []string{"-since"},
			cmdArgs: []string{"-since"},
		},
		{ // -- terminates flag parsing
			args:       []string{"-delete", "--", "prune", "-contacts"},
			cmdArgs:    []string{"-delete"},
			positional: []string{"prune", "-contacts"},
		},
	}

	for _, tc := range tcs {
		cmdArgs, cfgArgs, positional := runtime.SplitArgs(newFlags(), tc.args)

		assert.Equal(t, tc.cmdArgs, cmdArgs, "command args mismatch for args %v", tc.args)
		assert.Equal(t, tc.cfgArgs, cfgArgs, "config args mismatch for args %v", tc.args)
		assert.Equal(t, tc.positional, positional, "positional args mismatch for args %v", tc.args)
	}

	// check that the resulting args actually parse
	flags := newFlags()
	cmdArgs, cfgArgs, positional := runtime.SplitArgs(flags, []string{"-delete", "prune", "--db=postgres://temba@localhost/temba", "contacts"})
	assert.NoError(t, flags.Parse(cmdArgs))
	assert.Equal(t, "true", flags.Lookup("delete").Value.String())
	assert.Equal(t, []string{"prune", "contacts"}, positional)

	cfg, err := runtime.LoadConfig(runtime.NewDefaultConfig(), cfgArgs)
	assert.NoError(t, err)
	assert.Equal(t, "postgres://temba@localhost/temba", cfg.DB)
}
