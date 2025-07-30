package search_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeindexContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(t, testsuite.ResetAll)

	testsuite.ReindexElastic(t)

	// ensures changes are visible in elastic
	refreshElastic := func() {
		_, err := rt.ES.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(ctx)
		require.NoError(t, err)
	}

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE org_id = $1`, testdb.Org1.ID).Returns(124)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE org_id = $1`, testdb.Org2.ID).Returns(121)
	assertSearchCount(t, rt, elastic.Term("org_id", testdb.Org1.ID), 124)
	assertSearchCount(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	deindexed, err := search.DeindexContactsByID(ctx, rt, testdb.Org1.ID, []models.ContactID{testdb.Bob.ID, testdb.George.ID})
	assert.NoError(t, err)
	assert.Equal(t, 2, deindexed)

	refreshElastic()

	assertSearchCount(t, rt, elastic.Term("org_id", testdb.Org1.ID), 122)
	assertSearchCount(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	deindexed, err = search.DeindexContactsByOrg(ctx, rt, testdb.Org1.ID, 100)
	assert.NoError(t, err)
	assert.Equal(t, 100, deindexed)

	refreshElastic()

	assertSearchCount(t, rt, elastic.Term("org_id", testdb.Org1.ID), 22)
	assertSearchCount(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	deindexed, err = search.DeindexContactsByOrg(ctx, rt, testdb.Org1.ID, 100)
	assert.NoError(t, err)
	assert.Equal(t, 22, deindexed)

	refreshElastic()

	assertSearchCount(t, rt, elastic.Term("org_id", testdb.Org1.ID), 0)
	assertSearchCount(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	deindexed, err = search.DeindexContactsByOrg(ctx, rt, testdb.Org1.ID, 100)
	assert.NoError(t, err)
	assert.Equal(t, 0, deindexed)
}

func assertSearchCount(t *testing.T, rt *runtime.Runtime, query elastic.Query, expected int) {
	src := map[string]any{"query": query}

	resp, err := rt.ES.Count().Index(rt.Config.ElasticContactsIndex).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(context.Background())
	require.NoError(t, err)
	assert.Equal(t, expected, int(resp.Count))
}
