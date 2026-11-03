package crons

import (
	"context"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	Register("flush_tallies", &FlushTalliesCron{})
}

type FlushTalliesCron struct{}

func (c *FlushTalliesCron) Next(last time.Time) time.Time {
	return Next(last, 30*time.Second)
}

// Run flushes tallies buffered in valkey to the database
func (c *FlushTalliesCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	vc := rt.VK.Get()
	defer vc.Close()

	flushed, err := models.Tallies.Flush(ctx, vc, models.NewTallyStore(rt.DB), 1000)

	return map[string]any{"flushed": flushed}, err
}
