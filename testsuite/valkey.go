package testsuite

import "github.com/nyaruka/vkutil/assertvk"

// Each test claims its own valkey database, so that concurrent tests sharing a valkey can't interfere with each other.
// On a valkey with enough databases, claims are coordinated with those of other projects' tests.
func init() {
	assertvk.Coordinate(16, 17, 63)
}

// the base of the web server ports - each test gets a pair offset by its valkey database, which is unique to it
const portBase = 8200
