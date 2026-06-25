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

// SocketHistoryNamespace is the realtime pub/sub namespace for a contact's message history. A history socket is
// addressed as "history:<contact-uuid>" for a contact's whole history (the contact read page) or
// "history:<contact-uuid>:<ticket-uuid>" for the subset scoped to a single ticket (the ticket read page). Mailroom
// publishes engine events to these sockets for any live subscribers. ("Socket" is our name for a realtime pub/sub
// address - so as not to overload Channel, which already means a messaging channel.)
const SocketHistoryNamespace = "history"

// HistorySocket returns the realtime pub/sub socket for a contact's message history, optionally scoped to a single
// ticket. Given a ticket it returns that ticket's socket ("history:<contact-uuid>:<ticket-uuid>"), otherwise the
// contact's socket ("history:<contact-uuid>"). At most one ticket is used; any extra is ignored.
func HistorySocket(contactUUID flows.ContactUUID, ticketUUID ...flows.TicketUUID) string {
	if len(ticketUUID) > 0 {
		return fmt.Sprintf("%s:%s:%s", SocketHistoryNamespace, contactUUID, ticketUUID[0])
	}
	return fmt.Sprintf("%s:%s", SocketHistoryNamespace, contactUUID)
}

// subscriptionKey is the valkey key marking that a realtime socket has at least one active subscriber, e.g.
// "socket-subs:history:<contact-uuid>". The key is a per-socket presence marker written by the service that
// authorizes subscriptions (it sets/re-arms the key with a TTL on every subscribe and refresh); mailroom only
// reads it.
func subscriptionKey(socket string) string {
	return fmt.Sprintf("socket-subs:%s", socket)
}

// IsSubscribed reports whether a realtime socket currently has at least one active subscriber.
func IsSubscribed(ctx context.Context, rt *runtime.Runtime, socket string) (bool, error) {
	vc := rt.VK.Get()
	defer vc.Close()

	subscribed, err := valkey.Bool(valkey.DoContext(vc, ctx, "EXISTS", subscriptionKey(socket)))
	if err != nil {
		return false, fmt.Errorf("error checking subscription for %s: %w", socket, err)
	}
	return subscribed, nil
}

// PublishToHistory publishes engine events to a contact's history sockets for any live subscribers. Each event is
// sent as its full JSON, including its uuid - matching the shape clients fetch from the history table, save for the
// hydration the fetch layer adds on read (e.g. resolving user avatars).
//
// Events are routed to mirror how the read API filters the same events: the per-ticket detail events (assignee, note
// and topic changes) are filtered off the contact read page and so go only to that ticket's socket, while everything
// else - non-ticket events plus the basic ticket lifecycle events (opened/closed/reopened) - goes to the contact's
// socket. The ticket read page subscribes to both its ticket socket and the contact socket, so the union it sees
// matches what the read API returns for that ticket.
//
// Every subscribed socket's events are batched into a single pipelined request, so one commit costs one centrifugo
// round-trip no matter how many sockets it spans, and the whole batch lands or fails together. It's best-effort and
// a no-op for any socket that currently has no subscribers; we only publish to a socket when someone is watching.
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

	pipe := rt.Centrifugo.Pipe()
	var sockets []string // the socket each pipelined publish targets, parallel to the replies for error reporting

	// addEvents adds a socket's events to the shared pipe, but only if the socket currently has subscribers
	addEvents := func(socket string, evts []flows.Event) error {
		if len(evts) == 0 {
			return nil
		}
		subscribed, err := IsSubscribed(ctx, rt, socket)
		if err != nil {
			return err
		}
		if !subscribed {
			return nil
		}
		for _, e := range evts {
			data, err := json.Marshal(e)
			if err != nil {
				return fmt.Errorf("error marshaling event for %s: %w", socket, err)
			}
			if err := pipe.AddPublish(socket, data); err != nil {
				return fmt.Errorf("error adding event to publish pipe for %s: %w", socket, err)
			}
			sockets = append(sockets, socket)
		}
		return nil
	}

	if err := addEvents(HistorySocket(contactUUID), contactEvents); err != nil {
		return err
	}
	for ticketUUID, evts := range ticketEvents {
		if err := addEvents(HistorySocket(contactUUID, ticketUUID), evts); err != nil {
			return err
		}
	}

	if len(sockets) == 0 {
		return nil
	}

	replies, err := rt.Centrifugo.SendPipe(ctx, pipe)
	if err != nil {
		return fmt.Errorf("error publishing history events: %w", err)
	}
	for i, reply := range replies {
		if reply.Error != nil {
			return fmt.Errorf("error publishing event to %s: %w", sockets[i], reply.Error)
		}
	}

	return nil
}

// ticketDetailEvent returns the ticket UUID and true if the event is a per-ticket detail event - one the read API
// includes on the ticket page but filters off the contact page (assignee/note/topic changes). Everything else,
// including the basic ticket lifecycle events (opened/closed/reopened), belongs on the contact socket.
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
