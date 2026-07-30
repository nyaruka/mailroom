package org_test

import (
	"testing"

	"github.com/nyaruka/gocommon/centrifugo"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublish(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	vc := rt.VK.Get()
	defer vc.Close()
	orgSocket := models.OrgSocket(models.OrgUUID(testdb.Org1.UUID))
	_, err := vc.Do("SET", centrifugo.SubscriptionKey(orgSocket), "1")
	require.NoError(t, err)

	testsuite.RunWebTests(t, rt, "testdata/publish.json")

	sent := testsuite.CentrifugoHistory(t, rt, orgSocket)
	require.Len(t, sent, 1)
	assert.JSONEq(t, `{"type":"asset_changed","asset":{"type":"flow","uuid":"9de3663f-c5c5-4c92-9f45-ecbc09abcc85","name":"Registration"}}`, string(sent[0]))
}

func TestDeindex(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer func() {
		rt.DB.MustExec(`UPDATE orgs_org SET is_active = true WHERE id = $1`, testdb.Org1.ID)
	}()

	rt.DB.MustExec(`UPDATE orgs_org SET is_active = false WHERE id = $1`, testdb.Org1.ID)

	defer testsuite.Reset(t, rt, testsuite.ResetElastic|testsuite.ResetValkey)

	testsuite.RunWebTests(t, rt, "testdata/deindex.json")

	vc := rt.VK.Get()
	defer vc.Close()
	assertvk.SMembers(t, vc, "deindex:contacts", []string{"1"})
}
