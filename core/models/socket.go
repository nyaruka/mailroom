package models

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

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
// socket that currently has no subscribers - the centrifugo service resolves subscriber presence for the whole batch
// in a single lookup and sends the surviving notifications as one pipelined request, so the batch lands or fails
// together.
func PublishNotifications(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, notifications []*Notification) error {
	if len(notifications) == 0 {
		return nil
	}

	orgUUID := oa.Org().UUID()

	pubs := make([]*centrifugo.Publication, 0, len(notifications))
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
		pubs = append(pubs, &centrifugo.Publication{Channel: NotificationSocket(orgUUID, user.UUID()), Data: json.RawMessage(data)})
	}

	if err := rt.Centrifugo.Publish(ctx, pubs...); err != nil {
		return fmt.Errorf("error publishing notifications: %w", err)
	}

	return nil
}

// PublishNotificationData publishes already-rendered notification payloads to their users' notification sockets. It's
// the counterpart to PublishNotifications for notifications created outside mailroom (e.g. the platform's Django side
// creates finished-export and locally-detected-incident notifications): it delivers them over the same realtime path,
// reusing the socket addressing and subscriber-presence check so there's a single implementation of those. Each item's
// data is published verbatim - the rendering is the caller's, so mailroom needs no knowledge of those notification
// types. Each item carries its user's UUID so sockets are addressed directly without a user asset lookup - the user
// may not belong to the workspace (e.g. staff who serviced it). Best-effort and a no-op for any socket with no
// current subscribers, exactly like PublishNotifications.
func PublishNotificationData(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, items []NotificationData) error {
	if len(items) == 0 {
		return nil
	}

	orgUUID := oa.Org().UUID()

	pubs := make([]*centrifugo.Publication, 0, len(items))
	for _, it := range items {
		pubs = append(pubs, &centrifugo.Publication{Channel: NotificationSocket(orgUUID, it.UserUUID), Data: it.Data})
	}

	if err := rt.Centrifugo.Publish(ctx, pubs...); err != nil {
		return fmt.Errorf("error publishing notifications: %w", err)
	}

	return nil
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
// It's best-effort and a no-op for any socket that currently has no subscribers - the centrifugo service resolves
// subscriber presence for every socket the commit touches in a single lookup and sends the surviving events as one
// pipelined request, so a commit costs one centrifugo round-trip no matter how many sockets it spans, and the whole
// batch lands or fails together. Events are passed to the service unmarshaled, so in the common case where no
// socket has a subscriber they're dropped without ever paying the marshaling cost.
func PublishToHistory(ctx context.Context, rt *runtime.Runtime, contactUUID core.ContactUUID, evts []events.Event) error {
	pubs := make([]*centrifugo.Publication, len(evts))
	for i, e := range evts {
		socket := HistorySocket(contactUUID)
		if ticketUUID, ok := ticketDetailEvent(e); ok {
			socket = HistorySocket(contactUUID, ticketUUID)
		}
		pubs[i] = &centrifugo.Publication{Channel: socket, Data: e}
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
