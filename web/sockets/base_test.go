package sockets

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
)

func TestSubscribe(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	// a released (soft-deleted) contact in org1 - should be denied like a non-existent one
	released := testdb.InsertContact(
		t, rt, testdb.Org1, "11111111-1111-4111-8111-111111111111", "Released", "eng", models.ContactStatusActive,
	)
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = FALSE WHERE id = $1`, released.ID)

	// a blocked contact is still authorized - blocked/stopped/archived contacts keep viewable message history
	testdb.InsertContact(
		t, rt, testdb.Org1, "22222222-2222-4222-8222-222222222222", "Blocked", "eng", models.ContactStatusBlocked,
	)

	testsuite.RunWebTests(t, rt, "testdata/subscribe.json")

	vc := rt.VK.Get()
	defer vc.Close()

	annKey := "socket-subs:history:a393abc0-283d-4c9b-a1b3-641a035c34bf"
	blockedKey := "socket-subs:history:22222222-2222-4222-8222-222222222222"

	// the allowed subscribes marked their contact's history channel as having a subscriber...
	assertvk.Exists(t, vc, annKey)
	assertvk.Exists(t, vc, blockedKey)

	// ...and the denied/disconnected subscribes (other org, missing, malformed uuid, unknown namespace,
	// released contact, no/empty meta) wrote nothing - only the two allowed channels exist
	assertvk.Keys(t, vc, "socket-subs:*", []string{annKey, blockedKey})
}

func TestSubRefresh(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testsuite.RunWebTests(t, rt, "testdata/sub_refresh.json")

	vc := rt.VK.Get()
	defer vc.Close()

	// the successful refresh kept the channel marked; nothing else was written
	key := "socket-subs:history:a393abc0-283d-4c9b-a1b3-641a035c34bf"
	assertvk.Exists(t, vc, key)
	assertvk.Keys(t, vc, "socket-subs:*", []string{key})
}
