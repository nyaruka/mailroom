package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/vinovest/sqlx"
)

// Queryer lets us pass anything that supports QueryContext to a function (sql.DB, sql.Tx, sqlx.DB, sqlx.Tx)
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// DBorTx contains functionality common to sqlx.Tx and sqlx.DB so we can write code that works with either
type DBorTx interface {
	Queryer
	dbutil.BulkQueryer

	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	NamedExecContext(ctx context.Context, query string, arg any) (sql.Result, error)
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
	GetContext(ctx context.Context, value any, query string, args ...any) error
}

// DB is most of the functionality of sqlx.DB but lets us mock it in tests.
type DB interface {
	DBorTx

	BeginTxx(ctx context.Context, opts *sql.TxOptions) (*sqlx.Tx, error)
}

// BulkQuery runs the given query as a bulk operation
func BulkQuery[T any](ctx context.Context, label string, tx DBorTx, sql string, structs []T) error {
	// no values, nothing to do
	if len(structs) == 0 {
		return nil
	}

	start := time.Now()

	err := dbutil.BulkQuery(ctx, tx, sql, structs)
	if err != nil {
		return fmt.Errorf("error making bulk query: %w", err)
	}

	slog.Debug(fmt.Sprintf("%s bulk sql complete", label), "elapsed", time.Since(start), "rows", len(structs))

	return nil
}

// BulkQueryBatches runs the given query as a bulk operation, in batches of the given size
func BulkQueryBatches[T any](ctx context.Context, label string, tx DBorTx, sql string, batchSize int, structs []T) error {
	start := time.Now()

	i := 0
	for batch := range slices.Chunk(structs, batchSize) {
		err := dbutil.BulkQuery(ctx, tx, sql, batch)
		if err != nil {
			return fmt.Errorf("error making bulk batch query: %w", err)
		}

		slog.Debug(fmt.Sprintf("%s bulk sql batch complete", label), "elapsed", time.Since(start), "rows", len(batch), "batch", i)
		i++
	}

	return nil
}

func ScanJSONRows[T any](rows *sql.Rows, f func() T) ([]T, error) {
	defer rows.Close()

	as := make([]T, 0, 10)
	for rows.Next() {
		a := f()
		err := dbutil.ScanJSON(rows, &a)
		if err != nil {
			return nil, fmt.Errorf("error scanning into %T: %w", a, err)
		}
		as = append(as, a)
	}

	return as, nil
}

// JSONB is a generic wrapper for a type which should be written to and read from the database as JSON.
type JSONB[T any] struct {
	V T
}

func (t *JSONB[T]) Scan(value any) error {
	b, ok := value.([]byte)
	if !ok {
		return errors.New("failed type assertion to []byte")
	}
	return json.Unmarshal(b, &t.V)
}

func (t JSONB[T]) Value() (driver.Value, error) {
	return json.Marshal(t.V)
}

func StringArray[T ~string](vals []T) pq.StringArray {
	a := make(pq.StringArray, len(vals))
	for i := range vals {
		a[i] = string(vals[i])
	}
	return a
}

// Config is a util for reading config values stored as JSONB
type Config map[string]any

// GetString returns the value of the key as a string. If the key does not exist, it returns the default value.
func (c Config) GetString(key, def string) string {
	if v, ok := c[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// GetInt returns the value of the key as an int. If the key does not exist or cannot be converted to an int, it returns the default value.
func (c Config) GetInt(key string, def int) int {
	if v, ok := c[key]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
		if n, ok := v.(int); ok {
			return n
		}
		if s, ok := v.(string); ok {
			n, err := strconv.Atoi(s)
			if err == nil {
				return n
			}
		}
	}
	return def
}
