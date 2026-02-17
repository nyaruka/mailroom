package crons

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	Register("retry_calls", &RetryCallsCron{})
}

type RetryCallsCron struct{}

func (c *RetryCallsCron) Next(last time.Time) time.Time {
	return Next(last, time.Minute)
}

func (c *RetryCallsCron) AllInstances() bool {
	return false
}

// RetryCalls looks for calls that need to be retried and retries them
func (c *RetryCallsCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	log := slog.With("comp", "ivr_cron_retryer")

	// find all calls that need restarting
	ctx, cancel := context.WithTimeout(ctx, time.Minute*5)
	defer cancel()

	calls, err := models.LoadCallsToRetry(ctx, rt.DB, 100)
	if err != nil {
		return nil, fmt.Errorf("error loading calls to retry: %w", err)
	}

	throttledChannels := make(map[models.ChannelID]bool)
	clogs := make([]*models.ChannelLog, 0, len(calls))

	// schedules requests for each call
	for _, call := range calls {
		log = log.With("call", call.UUID())

		// if the channel for this call is throttled, move on
		if throttledChannels[call.ChannelID()] {
			call.SetThrottled(ctx, rt.DB)
			log.Info("skipping call, throttled", "channel_id", call.ChannelID())
			continue
		}

		// load the org for this call
		oa, err := models.GetOrgAssets(ctx, rt, call.OrgID())
		if err != nil {
			log.Error("error loading org", "error", err, "org_id", call.OrgID())
			continue
		}

		// and the associated channel
		channel := oa.ChannelByID(call.ChannelID())
		if channel == nil {
			if err := call.SetFailed(ctx, rt.DB); err != nil {
				log.Error("error marking call as failed due to missing channel", "error", err, "channel_id", call.ChannelID())
			}
			continue
		}

		// finally load the full URN
		cu, err := models.LoadContactURN(ctx, rt.DB, call.ContactURNID())
		if err != nil {
			log.Error("unable to load contact urn", "error", err, "urn_id", call.ContactURNID())
			continue
		}

		urn, _ := cu.Encode(oa)

		clog, err := ivr.RequestCallStart(ctx, rt, channel, urn, call)
		if clog != nil {
			clogs = append(clogs, clog)
		}
		if err != nil {
			log.Error("error requesting start for call", "error", err)
			continue
		}

		// queued status on a call we just tried means it is throttled, mark our channel as such
		throttledChannels[call.ChannelID()] = true
	}

	// log any error writing our channel logs, but continue
	for _, clog := range clogs {
		if _, err := rt.Dynamo.Main.Queue(clog); err != nil {
			slog.Error("error queuing IVR channel log to writer", "error", err, "log", clog.UUID)
		}
	}

	return map[string]any{"retried": len(calls)}, nil
}
