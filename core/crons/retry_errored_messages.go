package crons

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	Register("retry_errored_messages", &RetryMessagesCron{})
}

type RetryMessagesCron struct{}

func (c *RetryMessagesCron) Next(last time.Time) time.Time {
	return Next(last, time.Minute)
}

func (c *RetryMessagesCron) AllInstances() bool {
	return false
}

func (c *RetryMessagesCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	rc := rt.RP.Get()
	defer rc.Close()

	msgs, err := models.GetMessagesForRetry(ctx, rt.DB)
	if err != nil {
		return nil, fmt.Errorf("error fetching errored messages to retry: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil // nothing to retry
	}

	retries, err := models.PrepareMessagesForRetry(ctx, rt.DB, msgs)
	if err != nil {
		return nil, fmt.Errorf("error preparing messages for retrying: %w", err)
	}

	msgio.QueueMessages(ctx, rt, retries)

	return map[string]any{"retried": len(msgs)}, nil
}
