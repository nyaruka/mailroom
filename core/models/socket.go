package models

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// SocketHistoryNamespace is the realtime subscription channel namespace for a contact's message history,
// addressed as "history:<contact-uuid>". It's currently the only client-subscribable namespace. ("Channel"
// here is a realtime pub/sub channel - unrelated to a messaging Channel.)
const SocketHistoryNamespace = "history"

// HistoryChannel returns the realtime subscription channel for a contact's message history.
func HistoryChannel(contactUUID flows.ContactUUID) string {
	return fmt.Sprintf("%s:%s", SocketHistoryNamespace, contactUUID)
}

// subscriptionKey is the valkey key marking that a realtime channel has at least one active subscriber, e.g.
// "socket-subs:history:<contact-uuid>".
func subscriptionKey(channel string) string {
	return fmt.Sprintf("socket-subs:%s", channel)
}

// RecordSubscription marks a realtime channel as having at least one active subscriber by (re)setting a
// single per-channel presence key in valkey with the given TTL. The websocket layer calls this on every
// subscribe and refresh, so the key stays present while some subscriber keeps refreshing it and expires once
// the last one stops (the realtime server has no unsubscribe/disconnect callback, so the TTL is the only GC).
// We only track presence - whether a channel has any subscribers, not who or how many - so one key per
// channel is all we keep.
func RecordSubscription(ctx context.Context, rt *runtime.Runtime, channel string, ttl time.Duration) error {
	vc := rt.VK.Get()
	defer vc.Close()

	if _, err := valkey.DoContext(vc, ctx, "SET", subscriptionKey(channel), "1", "EX", int(ttl/time.Second)); err != nil {
		return fmt.Errorf("error recording subscription for %s: %w", channel, err)
	}
	return nil
}

// IsSubscribed reports whether a realtime channel currently has at least one active subscriber.
func IsSubscribed(ctx context.Context, rt *runtime.Runtime, channel string) (bool, error) {
	vc := rt.VK.Get()
	defer vc.Close()

	subscribed, err := valkey.Bool(valkey.DoContext(vc, ctx, "EXISTS", subscriptionKey(channel)))
	if err != nil {
		return false, fmt.Errorf("error checking subscription for %s: %w", channel, err)
	}
	return subscribed, nil
}

// PublishToHistory publishes engine events to a contact's history channel for any live subscribers. Each event
// is sent as its full JSON, including its uuid - matching the shape clients fetch from the history table, save
// for the hydration the fetch layer adds on read (e.g. resolving user avatars). It's best-effort and a no-op
// when realtime isn't configured or the channel currently has no subscribers; we only pay the centrifugo
// publish when someone is actually watching.
func PublishToHistory(ctx context.Context, rt *runtime.Runtime, contactUUID flows.ContactUUID, events []flows.Event) error {
	if rt.Centrifugo == nil || len(events) == 0 {
		return nil
	}

	channel := HistoryChannel(contactUUID)

	subscribed, err := IsSubscribed(ctx, rt, channel)
	if err != nil {
		return err
	}
	if !subscribed {
		return nil
	}

	// batch all events into a single pipelined request so a subscribed contact costs one round-trip per commit
	// regardless of how many events it produced, and the whole batch lands or fails together
	pipe := rt.Centrifugo.Pipe()
	for _, e := range events {
		data, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("error marshaling event for %s: %w", channel, err)
		}
		if err := pipe.AddPublish(channel, data); err != nil {
			return fmt.Errorf("error adding event to publish pipe for %s: %w", channel, err)
		}
	}

	replies, err := rt.Centrifugo.SendPipe(ctx, pipe)
	if err != nil {
		return fmt.Errorf("error publishing events to %s: %w", channel, err)
	}
	for _, reply := range replies {
		if reply.Error != nil {
			return fmt.Errorf("error publishing event to %s: %w", channel, reply.Error)
		}
	}

	return nil
}
