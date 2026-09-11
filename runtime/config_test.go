package runtime_test

import (
	"net"
	"testing"

	"github.com/nyaruka/mailroom/v26/runtime"

	"github.com/stretchr/testify/assert"
)

func TestConfigParse(t *testing.T) {
	// the defaults are valid
	assert.NoError(t, runtime.NewDefaultConfig().Parse())

	cfg := runtime.NewDefaultConfig()
	cfg.DB = "??"
	cfg.ReadonlyDB = "??"
	cfg.Valkey = "??"
	cfg.ElasticEndpoint = "??"
	assert.EqualError(t, cfg.Parse(), "invalid configuration: field 'DB' is not a valid URL, field 'ReadonlyDB' is not a valid URL, field 'Valkey' is not a valid URL, field 'ElasticEndpoint' is not a valid URL")

	cfg = runtime.NewDefaultConfig()
	cfg.DB = "mysql://temba:temba@postgres/temba"
	cfg.Valkey = "bluedis://valkey:6379/15"
	assert.EqualError(t, cfg.Parse(), "invalid configuration: field 'DB' must start with 'postgres:', field 'Valkey' must start with 'valkey:' or 'valkeys:'")

	// redis:// is a valid Valkey URL as far as the pool is concerned, but our config surface is Valkey named
	cfg = runtime.NewDefaultConfig()
	cfg.Valkey = "redis://valkey:6379/15"
	assert.EqualError(t, cfg.Parse(), "invalid configuration: field 'Valkey' must start with 'valkey:' or 'valkeys:'")

	// valkeys:// selects a TLS connection
	cfg = runtime.NewDefaultConfig()
	cfg.Valkey = "valkeys://valkey:6379/15"
	assert.NoError(t, cfg.Parse())
}

func TestDisallowedNetworksParsing(t *testing.T) {
	// check default value
	cfg := runtime.NewDefaultConfig()
	assert.NoError(t, cfg.Parse())

	mustParseCIDR := func(s string) *net.IPNet {
		_, n, perr := net.ParseCIDR(s)
		assert.NoError(t, perr)
		return n
	}

	ips, ipNets := cfg.DisallowedIPs, cfg.DisallowedNets
	assert.Equal(t, []net.IP{net.ParseIP(`::1`)}, ips)
	assert.Equal(t, []*net.IPNet{
		mustParseCIDR("127.0.0.0/8"),
		mustParseCIDR("fe80::/10"),
		mustParseCIDR("fc00::/7"),
		mustParseCIDR("10.0.0.0/8"),
		mustParseCIDR("172.16.0.0/12"),
		mustParseCIDR("192.168.0.0/16"),
		mustParseCIDR("100.64.0.0/10"),
		mustParseCIDR("169.254.0.0/16"),
		mustParseCIDR("0.0.0.0/8"),
	}, ipNets)

	// test with invalid network
	cfg = runtime.NewDefaultConfig()
	cfg.DisallowedNetworks = []string{`"127.0.0.1`}
	assert.Error(t, cfg.Parse())

	// test with single IP
	cfg = runtime.NewDefaultConfig()
	cfg.DisallowedNetworks = []string{`127.0.0.1`}
	assert.NoError(t, cfg.Parse())

	ips, ipNets = cfg.DisallowedIPs, cfg.DisallowedNets
	assert.Equal(t, []net.IP{net.IPv4(127, 0, 0, 1)}, ips)
	assert.Equal(t, []*net.IPNet{}, ipNets)
}

func TestIDObfuscationKeyParsing(t *testing.T) {
	// check default value
	cfg := runtime.NewDefaultConfig()
	assert.NoError(t, cfg.Parse())
	assert.Equal(t, [4]uint32{0x000A3B1C, 0x000D2E3F, 0x0001A2B3, 0x00C0FFEE}, cfg.IDObfuscationKeyParsed)

	cfg = runtime.NewDefaultConfig()
	cfg.IDObfuscationKey = "00000000000000000000000000000000"
	assert.NoError(t, cfg.Parse())
	assert.Equal(t, [4]uint32{0, 0, 0, 0}, cfg.IDObfuscationKeyParsed)

	cfg = runtime.NewDefaultConfig()
	cfg.IDObfuscationKey = "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"
	assert.NoError(t, cfg.Parse())
	assert.Equal(t, [4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF}, cfg.IDObfuscationKeyParsed)

	cfg = runtime.NewDefaultConfig()
	cfg.IDObfuscationKey = "not-hex"
	assert.Error(t, cfg.Parse())
}
