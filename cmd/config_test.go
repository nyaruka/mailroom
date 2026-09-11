package cmd

import (
	"flag"
	"log/slog"
	"testing"

	"github.com/nyaruka/ezconf"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	// caller can customize the base config..
	cfg := runtime.NewDefaultConfig()
	cfg.Domain = "example.com"
	cfg.WebhooksBlockedDomains = []string{"chat.example.com"}
	cfg.LogLevel = slog.LevelError

	require.NoError(t, loadConfig(cfg, []string{`--log-level=warn`}))
	assert.Equal(t, "example.com", cfg.Domain)
	assert.Equal(t, []string{"chat.example.com"}, cfg.WebhooksBlockedDomains)
	assert.Equal(t, slog.LevelWarn, cfg.LogLevel)

	// but explicitly set values still take precedence
	cfg = runtime.NewDefaultConfig()
	cfg.Domain = "example.com"
	cfg.WebhooksBlockedDomains = []string{"chat.example.com"}

	require.NoError(t, loadConfig(cfg, []string{`--domain=temba.io`}))
	assert.Equal(t, "temba.io", cfg.Domain)
	assert.Equal(t, []string{"chat.example.com"}, cfg.WebhooksBlockedDomains)

	// and the loaded config is parsed so it's ready to be used
	assert.Len(t, cfg.DisallowedNets, 9)
	assert.Equal(t, [4]uint32{0x000A3B1C, 0x000D2E3F, 0x0001A2B3, 0x00C0FFEE}, cfg.IDObfuscationKeyParsed)

	// invalid values are rejected
	err := loadConfig(runtime.NewDefaultConfig(), []string{`--disallowed-networks="127.0.0.1`})
	assert.Error(t, err)

	// as are values which fail validation
	err = loadConfig(runtime.NewDefaultConfig(), []string{`--db=mysql://temba:temba@postgres/temba`, `--valkey=bluedis://valkey:6379/15`})
	assert.EqualError(t, err, "invalid configuration: field 'DB' must start with 'postgres:', field 'Valkey' must start with 'valkey:' or 'valkeys:'")
}

func TestLoadConfigHelp(t *testing.T) {
	// asking for usage isn't a config error - usage has been shown and the sentinel tells the caller to exit cleanly
	err := loadConfig(runtime.NewDefaultConfig(), []string{`--help`})
	assert.ErrorIs(t, err, ezconf.ErrHelp)
	assert.ErrorIs(t, err, flag.ErrHelp)

	err = loadConfig(runtime.NewDefaultConfig(), []string{`-h`})
	assert.ErrorIs(t, err, ezconf.ErrHelp)

	// whereas an unknown flag comes back from ezconf as a real error rather than exiting the process
	err = loadConfig(runtime.NewDefaultConfig(), []string{`--not-a-flag`})
	assert.EqualError(t, err, "error loading configuration: flag provided but not defined: -not-a-flag")
	assert.NotErrorIs(t, err, ezconf.ErrHelp)
}

// wrappedConfig is how an app built on top of mailroom adds its own settings to the config
type wrappedConfig struct {
	runtime.Config

	APIKey string `validate:"required" help:"the key used to access the wrapping app's API"`
}

func TestLoadConfigEmbedded(t *testing.T) {
	t.Setenv("MAILROOM_API_KEY", "sesame")

	// the embedded config's fields and the wrapping struct's own fields are loaded from the same sources
	cfg := &wrappedConfig{Config: *runtime.NewDefaultConfig()}
	require.NoError(t, loadConfig(cfg, []string{`--domain=temba.io`}))
	assert.Equal(t, "temba.io", cfg.Domain)
	assert.Equal(t, "sesame", cfg.APIKey)

	// and the embedded config is parsed so it's ready to be handed to the service
	assert.Len(t, cfg.DisallowedNets, 9)
	assert.Equal(t, [4]uint32{0x000A3B1C, 0x000D2E3F, 0x0001A2B3, 0x00C0FFEE}, cfg.IDObfuscationKeyParsed)

	// validation covers the wrapping struct's own fields...
	t.Setenv("MAILROOM_API_KEY", "")
	cfg = &wrappedConfig{Config: *runtime.NewDefaultConfig()}
	err := loadConfig(cfg, []string{`--domain=temba.io`})
	assert.EqualError(t, err, "invalid configuration: field 'APIKey' is required")

	// ...as well as the embedded ones, which are reported by their path within the wrapping struct
	t.Setenv("MAILROOM_API_KEY", "sesame")
	cfg = &wrappedConfig{Config: *runtime.NewDefaultConfig()}
	err = loadConfig(cfg, []string{`--db=mysql://temba:temba@postgres/temba`})
	assert.EqualError(t, err, "invalid configuration: field 'Config.DB' must start with 'postgres:'")

	// and asking for usage still comes back as the ErrHelp sentinel
	cfg = &wrappedConfig{Config: *runtime.NewDefaultConfig()}
	err = loadConfig(cfg, []string{`--help`})
	assert.ErrorIs(t, err, ezconf.ErrHelp)
}
