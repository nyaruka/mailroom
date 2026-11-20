package runtime_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadOnlyConnsDiscarded(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// with a single connection, a new backend PID means the pool discarded the old connection
	rt.DB.SetMaxOpenConns(1)

	backendPID := func() int {
		var pid int
		require.NoError(t, rt.DB.GetContext(ctx, &pid, `SELECT pg_backend_pid()`))
		return pid
	}

	pid1 := backendPID()

	// a session made read-only behaves like a server which has become read-only
	rt.DB.MustExecContext(ctx, `SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY`)

	_, err := rt.DB.ExecContext(ctx, `DELETE FROM contacts_contactfire`)
	assert.ErrorContains(t, err, "read-only transaction")
	assert.NotEqual(t, pid1, backendPID())
}
