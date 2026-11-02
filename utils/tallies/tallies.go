// Package tallies buffers org-level totals of things which only go up in Valkey and flushes them into the orgs_tally
// table, so that frequent increments cost one row update per flush rather than a write each.
//
// Keys:
//   - tallies:pending - set of orgs with buffered tallies
//   - tallies:<org> - hash of scope to amount, incremented by writers
//   - tallies:<org>:flushing - hash being flushed, with a _gen field holding its flush generation
//
// There must only be one flusher at a time, so a flush must be cancelled before any lock which ensures that expires.
package tallies

import (
	"context"
	"fmt"
	"time"

	valkey "github.com/gomodule/redigo/redis"
)

const (
	pendingKey      = "tallies:pending"
	generationField = "_gen"
)

// OrgID is the identifier of the org that tallies belong to.
type OrgID int64

func liveKey(orgID OrgID) string     { return fmt.Sprintf("tallies:%d", orgID) }
func flushingKey(orgID OrgID) string { return liveKey(orgID) + ":flushing" }

// Daily returns the scope for the total of the given scope on the UTC day of the given time.
func Daily(scope string, t time.Time) string {
	return scope + "/" + t.UTC().Format(time.DateOnly)
}

// Increment adds the given amounts to the tallies of the given org.
func Increment(ctx context.Context, vc valkey.Conn, orgID OrgID, amounts map[string]int) error {
	if len(amounts) == 0 {
		return nil
	}
	if _, ok := amounts[generationField]; ok {
		return fmt.Errorf("%s is a reserved scope", generationField)
	}

	vc.Send("MULTI")
	for scope, amount := range amounts {
		vc.Send("HINCRBY", liveKey(orgID), scope, amount)
	}
	vc.Send("SADD", pendingKey, orgID)

	if _, err := valkey.DoContext(vc, ctx, "EXEC"); err != nil {
		return fmt.Errorf("error incrementing tallies for org %d: %w", orgID, err)
	}
	return nil
}
