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

// SubscribedSockets returns the subset of the given sockets that currently have at least one active subscriber. It
// resolves them all in a single round-trip by MGETting their presence keys, so checking many sockets at once (e.g. a
// contact plus all of its tickets) costs one lookup rather than one per socket. A socket is subscribed when its key
// is present; missing keys come back nil. The returned map only contains the subscribed sockets.
func SubscribedSockets(ctx context.Context, rt *runtime.Runtime, sockets ...string) (map[string]bool, error) {
	if len(sockets) == 0 {
		return nil, nil
	}

	keys := make([]any, len(sockets))
	for i, s := range sockets {
		keys[i] = subscriptionKey(s)
	}

	vc := rt.VK.Get()
	defer vc.Close()

	values, err := valkey.Values(valkey.DoContext(vc, ctx, "MGET", keys...))
	if err != nil {
		return nil, fmt.Errorf("error checking socket subscriptions: %w", err)
	}

	subscribed := make(map[string]bool, len(sockets))
	for i, v := range values {
		if v != nil {
			subscribed[sockets[i]] = true
		}
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

	// gather the sockets this commit touches - the contact socket plus one per ticket with detail events - so their
	// subscription state can be resolved in a single round-trip rather than one presence lookup per socket (which
	// matters once a commit can span many tickets, e.g. a bulk ticket operation)
	type batch struct {
		socket string
		events []flows.Event
	}
	batches := make([]batch, 0, len(ticketEvents)+1)
	if len(contactEvents) > 0 {
		batches = append(batches, batch{HistorySocket(contactUUID), contactEvents})
	}
	for ticketUUID, evts := range ticketEvents {
		batches = append(batches, batch{HistorySocket(contactUUID, ticketUUID), evts})
	}
	if len(batches) == 0 {
		return nil
	}

	candidates := make([]string, len(batches))
	for i, b := range batches {
		candidates[i] = b.socket
	}
	subscribed, err := SubscribedSockets(ctx, rt, candidates...)
	if err != nil {
		return err
	}

	// batch every subscribed socket's events into a single pipelined request, so the whole commit is one centrifugo
	// round-trip no matter how many sockets it spans, and the batch lands or fails together
	pipe := rt.Centrifugo.Pipe()
	var targets []string // the socket each pipelined publish targets, parallel to the replies for error reporting
	for _, b := range batches {
		if !subscribed[b.socket] {
			continue
		}
		for _, e := range b.events {
			data, err := json.Marshal(e)
			if err != nil {
				return fmt.Errorf("error marshaling event for %s: %w", b.socket, err)
			}
			if err := pipe.AddPublish(b.socket, data); err != nil {
				return fmt.Errorf("error adding event to publish pipe for %s: %w", b.socket, err)
			}
			targets = append(targets, b.socket)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	replies, err := rt.Centrifugo.SendPipe(ctx, pipe)
	if err != nil {
		return fmt.Errorf("error publishing history events: %w", err)
	}
	for i, reply := range replies {
		if reply.Error != nil {
			return fmt.Errorf("error publishing event to %s: %w", targets[i], reply.Error)
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
