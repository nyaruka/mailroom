package models

import (
	"context"
	"fmt"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// SocketChatNamespace is the realtime subscription channel namespace for a contact's chat history, addressed
// as "chat:<contact-uuid>". It's currently the only client-subscribable namespace. ("Channel" here is a
// realtime pub/sub channel - unrelated to a messaging Channel.)
const SocketChatNamespace = "chat"

// ChatChannel returns the realtime subscription channel for a contact's chat history.
func ChatChannel(contactUUID flows.ContactUUID) string {
	return fmt.Sprintf("%s:%s", SocketChatNamespace, contactUUID)
}

// subscriptionKey is the valkey key marking that a realtime channel has at least one active subscriber, e.g.
// "socket-subs:chat:<contact-uuid>".
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

// IsChatSubscribed reports whether a contact's chat history currently has any subscribers. Backend code that
// persists chat-visible activity (e.g. channel events) can use this to cheaply decide whether anyone is
// watching a given contact's chat.
func IsChatSubscribed(ctx context.Context, rt *runtime.Runtime, contactUUID flows.ContactUUID) (bool, error) {
	return IsSubscribed(ctx, rt, ChatChannel(contactUUID))
}
