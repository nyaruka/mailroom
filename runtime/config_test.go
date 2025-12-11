package runtime_test

import (
	"net"
	"testing"

	"github.com/nyaruka/mailroom/runtime"

	"github.com/stretchr/testify/assert"
)

func TestValidate(t *testing.T) {
	_, err := runtime.LoadConfig(`--db=??`, `--readonly-db=??`, `--valkey=??`, `--elastic=??`)
	assert.EqualError(t, err, "invalid configuration: field 'DB' is not a valid URL, field 'ReadonlyDB' is not a valid URL, field 'Valkey' is not a valid URL, field 'Elastic' is not a valid URL")

	_, err = runtime.LoadConfig(`--db=mysql://temba:temba@postgres/temba`, `--valkey=bluedis://valkey:6379/15`)
	assert.EqualError(t, err, "invalid configuration: field 'DB' must start with 'postgres:', field 'Valkey' must start with 'valkey:'")
}

func TestDisallowedNetworksParsing(t *testing.T) {
	// check default value
	cfg, err := runtime.LoadConfig(`--log-level=warn`)
	assert.NoError(t, err)
	assert.Equal(t, [4]uint32{0x000A3B1C, 0x000D2E3F, 0x0001A2B3, 0x00C0FFEE}, cfg.IDObfuscationKeyParsed)

	privateNetwork1 := &net.IPNet{IP: net.IPv4(10, 0, 0, 0).To4(), Mask: net.CIDRMask(8, 32)}
	privateNetwork2 := &net.IPNet{IP: net.IPv4(172, 16, 0, 0).To4(), Mask: net.CIDRMask(12, 32)}
	privateNetwork3 := &net.IPNet{IP: net.IPv4(192, 168, 0, 0).To4(), Mask: net.CIDRMask(16, 32)}

	linkLocalIPv4 := &net.IPNet{IP: net.IPv4(169, 254, 0, 0).To4(), Mask: net.CIDRMask(16, 32)}
	_, linkLocalIPv6, _ := net.ParseCIDR("fe80::/10")

	ips, ipNets := cfg.DisallowedIPs, cfg.DisallowedNets
	assert.NoError(t, err)
	assert.Equal(t, []net.IP{net.IPv4(127, 0, 0, 1), net.ParseIP(`::1`)}, ips)
	assert.Equal(t, []*net.IPNet{privateNetwork1, privateNetwork2, privateNetwork3, linkLocalIPv4, linkLocalIPv6}, ipNets)

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
