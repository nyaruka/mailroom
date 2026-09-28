package testsuite

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/require"
)

// Each test binary claims its own valkey database, so that concurrent test runs sharing a valkey can't interfere with
// each other. On a valkey with enough databases, claims are coordinated with those of other projects' tests.
func init() {
	assertvk.Coordinate(16, 17, 63)
}

// the base of the web server ports - each binary gets a pair offset by its valkey database, which is unique to it
const portBase = 8200

// testVKDB returns the DSN and number of this binary's valkey database
func testVKDB(t *testing.T) (string, int) {
	dsn := assertvk.TestDSN()

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	db, err := strconv.Atoi(strings.TrimPrefix(u.Path, "/"))
	require.NoError(t, err)

	return dsn, db
}
