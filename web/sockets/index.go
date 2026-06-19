package sockets

import (
	"context"
	"fmt"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// subsKey is the valkey key of the sorted set of active subscriptions to a channel.
func subsKey(channel string) string {
	return fmt.Sprintf("subs:%s", channel)
}

// indexSubscription records (or, when called from sub_refresh, extends) a client's subscription to a channel,
// so the backend can answer "which connections are subscribed to channel X". Each channel is a sorted set
// keyed by subsKey, whose members are connection ids scored by their expiry. We:
//
//   - ZADD the member with a score of now+TTL (extending it if already present),
//   - lazily prune members whose expiry has passed with ZREMRANGEBYSCORE, and
//   - set an EXPIRE on the whole key as a backstop so a channel nobody refreshes vanishes.
//
// The realtime server has no unsubscribe/disconnect callback, so this TTL + periodic refresh is the only
// reliable way to garbage collect subscriptions; the per-member score gives accurate per-connection expiry.
func indexSubscription(ctx context.Context, rt *runtime.Runtime, now time.Time, channel, client string) error {
	key := subsKey(channel)

	vc := rt.VK.Get()
	defer vc.Close()

	vc.Send("MULTI")
	vc.Send("ZADD", key, now.Add(subscribeTTL).Unix(), client)
	vc.Send("ZREMRANGEBYSCORE", key, 0, now.Unix())
	vc.Send("EXPIRE", key, int(subscribeTTL/time.Second))
	if _, err := valkey.DoContext(vc, ctx, "EXEC"); err != nil {
		return fmt.Errorf("error updating subscription index for %s: %w", channel, err)
	}

	return nil
}
