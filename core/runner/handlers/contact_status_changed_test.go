package handlers_test

import (
	"os"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestContactStatusChanged(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetDB)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{actions.NewSetContactStatus(flows.NewActionUUID(), flows.ContactStatusBlocked)},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'B'`,
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "contact_status_changed", "contact_groups_changed", "run_ended"},
				testdb.Bob.UUID: {"run_started", "run_ended"},
				testdb.Cat.UUID: {"run_started", "run_ended"},
				testdb.Dan.UUID: {"run_started", "run_ended"},
			},
		},
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{actions.NewSetContactStatus(flows.NewActionUUID(), flows.ContactStatusStopped)},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'S'`,
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "contact_status_changed", "run_ended"},
				testdb.Bob.UUID: {"run_started", "run_ended"},
				testdb.Cat.UUID: {"run_started", "run_ended"},
				testdb.Dan.UUID: {"run_started", "run_ended"},
			},
		},
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{actions.NewSetContactStatus(flows.NewActionUUID(), flows.ContactStatusActive)},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'A'`,
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
				{
					Query:   `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'A'`,
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "contact_status_changed", "run_ended"},
				testdb.Bob.UUID: {"run_started", "run_ended"},
				testdb.Cat.UUID: {"run_started", "run_ended"},
				testdb.Dan.UUID: {"run_started", "run_ended"},
			},
		},
	}

	f, err := os.Create("testdata/contact_status_changed.json")
	require.NoError(t, err)
	defer f.Close()
	f.Write(jsonx.MustMarshal(tcs))

	runTests(t, rt, "testdata/contact_status_changed.json")
}
