package runtime_test

import (
	"net"
	"testing"

	"github.com/nyaruka/mailroom/v26/runtime"

	"github.com/stretchr/testify/assert"
)

func TestValidate(t *testing.T) {
	_, err := runtime.LoadConfig(`--db=??`, `--readonly-db=??`, `--valkey=??`, `--elastic-endpoint=??`)
	assert.EqualError(t, err, "invalid configuration: field 'DB' is not a valid URL, field 'ReadonlyDB' is not a valid URL, field 'Valkey' is not a valid URL, field 'ElasticEndpoint' is not a valid URL")

	_, err = runtime.LoadConfig(`--db=mysql://temba:temba@postgres/temba`, `--valkey=bluedis://valkey:6379/15`)
	assert.EqualError(t, err, "invalid configuration: field 'DB' must start with 'postgres:', field 'Valkey' must start with 'valkey:'")
}

func TestDisallowedNetworksParsing(t *testing.T) {
	// check default value
	cfg, err := runtime.LoadConfig(`--log-level=warn`)
	assert.NoError(t, err)

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

	// test with invalid CSV
	_, err = runtime.LoadConfig(`--disallowed-networks="127.0.0.1`)
	assert.Error(t, err)

	// test with single IP
	cfg, err = runtime.LoadConfig(`--disallowed-networks="127.0.0.1"`)
	assert.NoError(t, err)

	ips, ipNets = cfg.DisallowedIPs, cfg.DisallowedNets
	assert.NoError(t, err)
	assert.Equal(t, []net.IP{net.IPv4(127, 0, 0, 1)}, ips)
	assert.Equal(t, []*net.IPNet{}, ipNets)
}

func TestIDObfuscationKeyParsing(t *testing.T) {
	// check default value
	cfg, err := runtime.LoadConfig("--log-level=warn")
	assert.NoError(t, err)
	assert.Equal(t, [4]uint32{0x000A3B1C, 0x000D2E3F, 0x0001A2B3, 0x00C0FFEE}, cfg.IDObfuscationKeyParsed)

	cfg, err = runtime.LoadConfig("--id-obfuscation-key=00000000000000000000000000000000")
	assert.NoError(t, err)
	assert.Equal(t, [4]uint32{0, 0, 0, 0}, cfg.IDObfuscationKeyParsed)

	cfg, err = runtime.LoadConfig("--id-obfuscation-key=FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF")
	assert.NoError(t, err)
	assert.Equal(t, [4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF}, cfg.IDObfuscationKeyParsed)
}
