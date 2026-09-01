package runtime

import (
	"flag"
	"reflect"
	"strings"
	"sync"

	"github.com/nyaruka/ezconf"
)

// interface implemented by flag values which don't take a value, e.g. -delete rather than -delete=true
type boolFlag interface {
	IsBoolFlag() bool
}

// SplitArgs splits the given command line arguments into those for the given command specific flag set, those for
// the configuration loader, and the positional arguments. Commands which take positional arguments can't just parse
// their own flags because flag.Parse stops at the first positional argument, and can't let the configuration loader
// parse everything because it rejects flags it doesn't know about. So we split first:
//
//	mrelastic -delete --db=postgres://... prune contacts
//	   cmd:  [-delete]  config: [--db=postgres://...]  positional: [prune contacts]
//
// Flags can be given before or after the positional arguments, in either -name=value or -name value form. Flags which
// aren't ours and aren't a known config flag are passed to the config loader, which reports them as unknown.
func SplitArgs(cmdFlags *flag.FlagSet, args []string) (cmdArgs, cfgArgs, positional []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// -- terminates flag parsing, everything after it is positional
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}

		name, hasValue := flagName(arg)
		if name == "" {
			positional = append(positional, arg)
			continue
		}

		dest := &cfgArgs
		takesValue := configFlagTakesValue(name)

		if f := cmdFlags.Lookup(name); f != nil {
			dest = &cmdArgs
			bf, isBool := f.Value.(boolFlag)
			takesValue = !isBool || !bf.IsBoolFlag()
		}

		*dest = append(*dest, arg)

		// a flag which takes a value but wasn't given one takes the next argument as its value
		if takesValue && !hasValue && i+1 < len(args) {
			i++
			*dest = append(*dest, args[i])
		}
	}
	return cmdArgs, cfgArgs, positional
}

// flagName returns the name of the flag the given argument sets, and whether it includes its value, or an empty name
// if the argument isn't a flag at all.
func flagName(arg string) (string, bool) {
	if len(arg) < 2 || arg[0] != '-' {
		return "", false
	}
	name := strings.TrimPrefix(arg[1:], "-")
	if name == "" { // i.e. arg was "--"
		return "", false
	}
	name, _, hasValue := strings.Cut(name, "=")
	return name, hasValue
}

// configFlagTakesValue reports whether the named config flag needs a value, so that we know whether the argument
// following it belongs to it. Flags we don't recognize are assumed not to, leaving what follows as positional - the
// config loader will report the flag itself as unknown.
func configFlagTakesValue(name string) bool {
	takesValue, exists := configFlags()[name]
	return exists && takesValue
}

// the config flags built from the Config struct in the same way the config loader builds them, mapped to whether
// they take a value
var configFlags = sync.OnceValue(func() map[string]bool {
	flags := make(map[string]bool)

	t := reflect.TypeOf(Config{})
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Tag.Get("name")
		if name == "" {
			name = ezconf.CamelToSnake(f.Name)
		}
		flags[strings.ReplaceAll(name, "_", "-")] = f.Type.Kind() != reflect.Bool
	}
	return flags
})
