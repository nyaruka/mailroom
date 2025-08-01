package org_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
)

func TestDeindex(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer func() {
		rt.DB.MustExec(`UPDATE orgs_org SET is_active = true WHERE id = $1`, testdb.Org1.ID)
	}()

	rt.DB.MustExec(`UPDATE orgs_org SET is_active = false WHERE id = $1`, testdb.Org1.ID)

	defer testsuite.Reset(t, rt, testsuite.ResetElastic|testsuite.ResetValkey)

	testsuite.RunWebTests(t, ctx, rt, "testdata/deindex.json", testsuite.ResetNone)

	vc := rt.VK.Get()
	defer vc.Close()
	assertvk.SMembers(t, vc, "deindex:contacts", []string{"1"})
}
