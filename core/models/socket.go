package models

import (
	"context"
	"encoding/json"
	"fmt"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// SocketHistoryNamespace is the realtime pub/sub channel namespace for a contact's message history. It's addressed
// as "history:<contact-uuid>" for a contact's whole history (the contact read page) or
// "history:<contact-uuid>:<ticket-uuid>" for the subset scoped to a single ticket (the ticket read page). Mailroom
// publishes engine events to these channels for any live subscribers. ("Channel" here is a realtime pub/sub channel
// - unrelated to a messaging Channel.)
const SocketHistoryNamespace = "history"

// HistoryChannel returns the realtime pub/sub channel for a contact's message history.
func HistoryChannel(contactUUID flows.ContactUUID) string {
	return fmt.Sprintf("%s:%s", SocketHistoryNamespace, contactUUID)
}

// TicketHistoryChannel returns the realtime pub/sub channel for a contact's history scoped to a single ticket.
func TicketHistoryChannel(contactUUID flows.ContactUUID, ticketUUID flows.TicketUUID) string {
	return fmt.Sprintf("%s:%s:%s", SocketHistoryNamespace, contactUUID, ticketUUID)
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

// PublishToHistory publishes engine events to a contact's history channels for any live subscribers. Each event is
// sent as its full JSON, including its uuid - matching the shape clients fetch from the history table, save for the
// hydration the fetch layer adds on read (e.g. resolving user avatars).
//
// Events are routed to mirror how the read API filters the same events: the per-ticket detail events (assignee, note
// and topic changes) are filtered off the contact read page and so go only to that ticket's channel, while everything
// else - non-ticket events plus the basic ticket lifecycle events (opened/closed/reopened) - goes to the contact's
// channel. The ticket read page subscribes to both its ticket channel and the contact channel, so the union it sees
// matches what the read API returns for that ticket.
//
// It's best-effort and a no-op for any channel that currently has no subscribers; we only pay the centrifugo publish
// when someone is watching.
func PublishToHistory(ctx context.Context, rt *runtime.Runtime, contactUUID flows.ContactUUID, events []flows.Event) error {
	if len(events) == 0 {
		return nil
	}

	contactEvents := make([]flows.Event, 0, len(events))
	ticketEvents := make(map[flows.TicketUUID][]flows.Event)

	for _, e := range events {
		if ticketUUID, ok := ticketDetailEvent(e); ok {
			ticketEvents[ticketUUID] = append(ticketEvents[ticketUUID], e)
		} else {
			contactEvents = append(contactEvents, e)
		}
	}

	if err := publishToChannel(ctx, rt, HistoryChannel(contactUUID), contactEvents); err != nil {
		return err
	}
	for ticketUUID, evts := range ticketEvents {
		if err := publishToChannel(ctx, rt, TicketHistoryChannel(contactUUID, ticketUUID), evts); err != nil {
			return err
		}
	}

	return nil
}

// ticketDetailEvent returns the ticket UUID and true if the event is a per-ticket detail event - one the read API
// includes on the ticket page but filters off the contact page (assignee/note/topic changes). Everything else,
// including the basic ticket lifecycle events (opened/closed/reopened), belongs on the contact channel.
func ticketDetailEvent(e flows.Event) (flows.TicketUUID, bool) {
	switch typed := e.(type) {
	case *events.TicketAssigneeChanged:
		return typed.TicketUUID, true
	case *events.TicketNoteAdded:
		return typed.TicketUUID, true
	case *events.TicketTopicChanged:
		return typed.TicketUUID, true
	default:
		return "", false
	}
}

// publishToChannel publishes events to a single realtime channel if it currently has subscribers. All the events are
// batched into one pipelined request so a subscribed channel costs one round-trip per commit regardless of how many
// events it produced, and the whole batch lands or fails together.
func publishToChannel(ctx context.Context, rt *runtime.Runtime, channel string, events []flows.Event) error {
	if len(events) == 0 {
		return nil
	}

	subscribed, err := IsSubscribed(ctx, rt, channel)
	if err != nil {
		return err
	}
	if !subscribed {
		return nil
	}

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
