package models

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/centrifugo"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
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
func HistorySocket(contactUUID core.ContactUUID, ticketUUID ...core.TicketUUID) string {
	if len(ticketUUID) > 0 {
		return fmt.Sprintf("%s:%s:%s", SocketHistoryNamespace, contactUUID, ticketUUID[0])
	}
	return fmt.Sprintf("%s:%s", SocketHistoryNamespace, contactUUID)
}

// SocketNotificationsNamespace is the realtime pub/sub namespace for a user's notifications within a workspace. A
// notification socket is addressed as "notifications:<org-uuid>:<user-uuid>". Like history sockets it's a client
// subscription, authorized per-session by the subscribe proxy, which records the same "socket-subs:" presence key -
// so mailroom only publishes to it when someone is watching.
const SocketNotificationsNamespace = "notifications"

// NotificationSocket returns the realtime pub/sub socket for a user's notifications within a workspace, addressed as
// "notifications:<org-uuid>:<user-uuid>".
func NotificationSocket(orgUUID OrgUUID, userUUID assets.UserUUID) string {
	return fmt.Sprintf("%s:%s:%s", SocketNotificationsNamespace, orgUUID, userUUID)
}

// PublishNotifications publishes the given notifications to their users' notification sockets, each as the same JSON a
// client would otherwise fetch from the notifications API. As with history, it's best-effort and a no-op for any
// socket that currently has no subscribers; the sockets this commit touches are resolved in a single presence lookup,
// then every subscribed socket's notifications are batched into one pipelined request so it costs one centrifugo
// round-trip.
func PublishNotifications(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, notifications []*Notification) error {
	if len(notifications) == 0 {
		return nil
	}

	orgUUID := oa.Org().UUID()

	msgs := make([]socketMessage, 0, len(notifications))
	for _, n := range notifications {
		user := oa.UserByID(n.UserID)
		if user == nil {
			slog.Error("unable to publish notification for unknown user", "user_id", n.UserID, "org_id", n.OrgID)
			continue
		}

		data, err := n.marshalForSocket()
		if err != nil {
			return fmt.Errorf("error marshaling notification for user #%d: %w", n.UserID, err)
		}
		msgs = append(msgs, socketMessage{NotificationSocket(orgUUID, user.UUID()), data})
	}

	return publishToSockets(ctx, rt, msgs)
}

// PublishNotificationData publishes already-rendered notification payloads to their users' notification sockets. It's
// the counterpart to PublishNotifications for notifications created outside mailroom (e.g. the platform's Django side
// creates finished-export and locally-detected-incident notifications): it delivers them over the same realtime path,
// reusing the socket addressing and subscriber-presence check so there's a single implementation of those. Each item's
// data is published verbatim - the rendering is the caller's, so mailroom needs no knowledge of those notification
// types. Best-effort and a no-op for any socket with no current subscribers, exactly like PublishNotifications.
func PublishNotificationData(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, items []NotificationData) error {
	if len(items) == 0 {
		return nil
	}

	orgUUID := oa.Org().UUID()

	msgs := make([]socketMessage, 0, len(items))
	for _, it := range items {
		user := oa.UserByID(it.UserID)
		if user == nil {
			slog.Error("unable to publish notification for unknown user", "user_id", it.UserID, "org_id", oa.OrgID())
			continue
		}
		msgs = append(msgs, socketMessage{NotificationSocket(orgUUID, user.UUID()), it.Data})
	}

	return publishToSockets(ctx, rt, msgs)
}

// socketMessage is an already-marshaled payload bound for a single realtime socket.
type socketMessage struct {
	socket string
	data   []byte
}

// publishToSockets publishes pre-rendered messages to their sockets, best-effort, skipping any socket that currently
// has no active subscriber. Subscriber presence for every socket is resolved in a single round-trip, then each
// subscribed socket's payload is batched into one pipelined centrifugo request so the whole batch costs one round-trip
// and lands or fails together. This is the shared publish core behind the notification publishers above.
func publishToSockets(ctx context.Context, rt *runtime.Runtime, msgs []socketMessage) error {
	if len(msgs) == 0 {
		return nil
	}

	candidates := make([]string, len(msgs))
	for i, m := range msgs {
		candidates[i] = m.socket
	}

	subscribed, err := SubscribedSockets(ctx, rt, candidates...)
	if err != nil {
		return err
	}

	pubs := make([]*centrifugo.Publish, 0, len(msgs))
	for _, m := range msgs {
		if subscribed[m.socket] {
			pubs = append(pubs, &centrifugo.Publish{Channel: m.socket, Data: m.data})
		}
	}

	if err := rt.Centrifugo.Publish(ctx, pubs...); err != nil {
		return fmt.Errorf("error publishing notifications: %w", err)
	}

	return nil
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
func PublishToHistory(ctx context.Context, rt *runtime.Runtime, contactUUID core.ContactUUID, evts []events.Event) error {
	if len(evts) == 0 {
		return nil
	}

	contactEvents := make([]events.Event, 0, len(evts))
	ticketEvents := make(map[core.TicketUUID][]events.Event)

	for _, e := range evts {
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
		events []events.Event
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
	var pubs []*centrifugo.Publish
	for _, b := range batches {
		if !subscribed[b.socket] {
			continue
		}
		for _, e := range b.events {
			data, err := json.Marshal(e)
			if err != nil {
				return fmt.Errorf("error marshaling event for %s: %w", b.socket, err)
			}
			pubs = append(pubs, &centrifugo.Publish{Channel: b.socket, Data: data})
		}
	}

	if err := rt.Centrifugo.Publish(ctx, pubs...); err != nil {
		return fmt.Errorf("error publishing history events: %w", err)
	}

	return nil
}

// ticketDetailEvent returns the ticket UUID and true if the event is a per-ticket detail event - one the read API
// includes on the ticket page but filters off the contact page (assignee/note/topic changes). Everything else,
// including the basic ticket lifecycle events (opened/closed/reopened), belongs on the contact socket.
func ticketDetailEvent(e events.Event) (core.TicketUUID, bool) {
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
