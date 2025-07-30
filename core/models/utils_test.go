package models_test

import (
	"encoding/json"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"

	"github.com/stretchr/testify/assert"
)

func TestBulkQueryBatches(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer rt.DB.MustExec(`DROP TABLE foo;`)

	rt.DB.MustExec(`CREATE TABLE foo (id serial NOT NULL PRIMARY KEY, name TEXT, age INT)`)

	type foo struct {
		ID   int    `db:"id"`
		Name string `db:"name"`
		Age  int    `db:"age"`
	}

	sql := `INSERT INTO foo (name, age) VALUES(:name, :age) RETURNING id`

	// noop with zero structs
	err := models.BulkQueryBatches(ctx, "foo inserts", rt.DB, sql, 10, []any{})
	assert.NoError(t, err)

	// test when structs fit into one batch
	foo1 := &foo{Name: "A", Age: 30}
	foo2 := &foo{Name: "B", Age: 31}
	err = models.BulkQueryBatches(ctx, "foo inserts", rt.DB, sql, 2, []any{foo1, foo2})
	assert.NoError(t, err)
	assert.Equal(t, 1, foo1.ID)
	assert.Equal(t, 2, foo2.ID)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM foo WHERE name = 'A' AND age = 30`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM foo WHERE name = 'B' AND age = 31`).Returns(1)

	// test when multiple batches are required
	foo3 := &foo{Name: "C", Age: 32}
	foo4 := &foo{Name: "D", Age: 33}
	foo5 := &foo{Name: "E", Age: 34}
	foo6 := &foo{Name: "F", Age: 35}
	foo7 := &foo{Name: "G", Age: 36}
	err = models.BulkQueryBatches(ctx, "foo inserts", rt.DB, sql, 2, []any{foo3, foo4, foo5, foo6, foo7})
	assert.NoError(t, err)
	assert.Equal(t, 3, foo3.ID)
	assert.Equal(t, 4, foo4.ID)
	assert.Equal(t, 5, foo5.ID)
	assert.Equal(t, 6, foo6.ID)
	assert.Equal(t, 7, foo7.ID)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM foo WHERE name = 'C' AND age = 32`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM foo WHERE name = 'D' AND age = 33`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM foo WHERE name = 'E' AND age = 34`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM foo WHERE name = 'F' AND age = 35`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM foo WHERE name = 'G' AND age = 36`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM foo `).Returns(7)
}

func TestJSONB(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer rt.DB.MustExec(`DROP TABLE foo;`)

	rt.DB.MustExec(`CREATE TABLE foo (id serial NOT NULL PRIMARY KEY, value JSONB NULL)`)

	type fooValue struct {
		Name string `json:"name"`
	}

	type foo struct {
		ID    int                    `db:"id"`
		Value models.JSONB[fooValue] `db:"value"`
	}

	foo1 := &foo{Value: models.JSONB[fooValue]{fooValue{Name: "A"}}}

	err := models.BulkQuery(ctx, "inserting foo", rt.DB, `INSERT INTO foo (value) VALUES(:value) RETURNING id`, []*foo{foo1})
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM foo WHERE value->>'name' = 'A'`).Returns(1)

	sqlSelect := `SELECT id, value FROM foo WHERE id = $1`

	foo2 := &foo{}
	err = rt.DB.GetContext(ctx, foo2, sqlSelect, foo1.ID)
	assert.NoError(t, err)
	assert.NotNil(t, foo2.Value)
	assert.Equal(t, "A", foo2.Value.V.Name)
}

func TestConfig(t *testing.T) {
	var cfg models.Config
	err := json.Unmarshal([]byte(`{"foo": "bar", "count": 234}`), &cfg)
	assert.NoError(t, err)

	cfg["integer"] = 345  // an actual int
	cfg["numstr"] = "456" // int as string

	assert.Equal(t, "bar", cfg.GetString("foo", "default"))
	assert.Equal(t, "default", cfg.GetString("count", "default"))
	assert.Equal(t, "default", cfg.GetString("integer", "default"))
	assert.Equal(t, "456", cfg.GetString("numstr", "default"))
	assert.Equal(t, "default", cfg.GetString("xxx", "default"))
	assert.Equal(t, 123, cfg.GetInt("foo", 123))
	assert.Equal(t, 234, cfg.GetInt("count", 123))
	assert.Equal(t, 345, cfg.GetInt("integer", 123))
	assert.Equal(t, 456, cfg.GetInt("numstr", 123))
	assert.Equal(t, 123, cfg.GetInt("xxx", 123))

}
