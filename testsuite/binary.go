package testsuite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sync"
	"testing"

	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/require"
)

// Some resources are per-binary rather than per-test: the elastic indexes, dynamo tables and attachments
// bucket are namespaced with a random process identifier and created on first use - see elastic.go,
// dynamo.go and storage.go. Ownership is marked with a Postgres advisory lock held for the binary's
// lifetime, so it evaporates when the run that owns it dies - and setup sweeps away the resources of any
// run which has died.

// random per-binary identifier used in per-binary resource names. Can't use the pid because binaries run
// in per-worktree containers which share the backing services, so pids are neither unique nor observable
// across them.
var binProcID = sync.OnceValue(func() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
})

// matches a process identifier segment in a per-binary resource name
var binProcIDRegex = regexp.MustCompile(`^[0-9a-f]{8}$`)

// binKey derives the advisory lock key which marks a binary's per-binary resources as in use
func binKey(procID string) int64 {
	return dbKey("binary_" + procID)
}

var binSetup sync.Once
var binSetupErr error

// ensureBinaryResources creates this binary's per-binary resources on first use
func ensureBinaryResources(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	binSetup.Do(func() { binSetupErr = setupBinaryResources(rt) })
	require.NoError(t, binSetupErr, "error setting up per-binary resources")
}

// setupBinaryResources claims this binary's resources, creates them, and sweeps those of dead runs
func setupBinaryResources(rt *runtime.Runtime) error {
	ctx := context.Background()

	owner, err := dbOwner()
	if err != nil {
		return err
	}

	// claim before any of the resources exist, so they're never visible to another binary's sweep unclaimed
	if _, err := owner.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, binKey(binProcID())); err != nil {
		return fmt.Errorf("error claiming binary resources: %w", err)
	}

	if err := setupStorage(ctx, rt); err != nil {
		return err
	}
	if err := setupElastic(ctx, rt); err != nil {
		return err
	}
	return setupDynamo(ctx, rt)
}

// sweepDeadBinaries deletes per-binary resources, given as names grouped by process identifier, belonging
// to runs which are no longer around. A resource still in use is protected by its owner's advisory lock,
// so any process identifier we can lock ourselves belongs to a dead run. Our own identifier, and anything
// that doesn't look like one, is ignored.
func sweepDeadBinaries(ctx context.Context, byProcID map[string][]string, del func(name string) error) error {
	admin, err := dbAdmin()
	if err != nil {
		return err
	}
	conn, err := admin.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	for procID, names := range byProcID {
		if procID == binProcID() || !binProcIDRegex.MatchString(procID) {
			continue // ours, or not a per-binary test resource
		}

		var got bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, binKey(procID)).Scan(&got); err != nil {
			return err
		}
		if !got {
			continue // in use by a live run
		}

		for _, name := range names {
			if err := del(name); err != nil {
				return err
			}
		}

		conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, binKey(procID))
	}

	return nil
}
