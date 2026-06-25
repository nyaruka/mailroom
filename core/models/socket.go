package models

import (
	"context"
	"encoding/json"
	"fmt"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// SocketHistoryNamespace is the realtime pub/sub channel namespace for a contact's message history, addressed
// as "history:<contact-uuid>". Mailroom publishes engine events to this channel for any live subscribers.
// ("Channel" here is a realtime pub/sub channel - unrelated to a messaging Channel.)
const SocketHistoryNamespace = "history"

// HistoryChannel returns the realtime pub/sub channel for a contact's message history.
func HistoryChannel(contactUUID flows.ContactUUID) string {
	return fmt.Sprintf("%s:%s", SocketHistoryNamespace, contactUUID)
}

// subscriptionKey is the valkey key marking that a realtime channel has at least one active subscriber, e.g.
// "socket-subs:history:<contact-uuid>". The key is a per-channel presence marker written by the service that
// authorizes subscriptions (it sets/re-arms the key with a TTL on every subscribe and refresh); mailroom only
// reads it.
func subscriptionKey(channel string) string {
	return fmt.Sprintf("socket-subs:%s", channel)
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
// when the channel currently has no subscribers; we only pay the centrifugo publish when someone is watching.
func PublishToHistory(ctx context.Context, rt *runtime.Runtime, contactUUID flows.ContactUUID, events []flows.Event) error {
	if len(events) == 0 {
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
