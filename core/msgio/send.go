package msgio

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

type contactAndChannel struct {
	contactID models.ContactID
	channel   *models.Channel
}

// QueueMessages tries to queue the given messages to courier or trigger Android channel syncs
func QueueMessages(ctx context.Context, rt *runtime.Runtime, msgs []*models.MsgOut) {
	queued := tryToQueue(ctx, rt, msgs)

	if len(queued) != len(msgs) {
		retry := make([]*models.Msg, 0, len(msgs)-len(queued))
		for _, m := range msgs {
			if !slices.Contains(queued, m) {
				retry = append(retry, m.Msg)
			}
		}

		// any messages that failed to queue should be moved back to initializing(I) (they are queued(Q) at creation to
		// save an update in the common case)
		if err := models.MarkMessagesForRequeuing(ctx, rt.DB, retry); err != nil {
			slog.Error("error marking messages as initializing", "error", err)
		}
	}
}

func tryToQueue(ctx context.Context, rt *runtime.Runtime, msgs []*models.MsgOut) []*models.MsgOut {
	if err := fetchMissingURNs(ctx, rt, msgs); err != nil {
		slog.Error("error fetching missing contact URNs", "error", err)
		return nil
	}

	// messages that have been successfully queued
	queued := make([]*models.MsgOut, 0, len(msgs))

	// organize what we have to send by org
	byOrg := make(map[models.OrgID][]*models.MsgOut)
	for _, m := range msgs {
		orgID := m.OrgID()
		byOrg[orgID] = append(byOrg[orgID], m)
	}

	for orgID, orgMsgs := range byOrg {
		oa, err := models.GetOrgAssets(ctx, rt, orgID)
		if err != nil {
			slog.Error("error getting org assets", "error", err)
		} else {
			queued = append(queued, tryToQueueForOrg(ctx, rt, oa, orgMsgs)...)
		}
	}

	return queued
}

func tryToQueueForOrg(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, msgs []*models.MsgOut) []*models.MsgOut {
	// sends by courier, organized by contact+channel
	courierMsgs := make(map[contactAndChannel][]*models.MsgOut, 100)

	// android channels that need to be notified to sync
	androidMsgs := make(map[*models.Channel][]*models.MsgOut, 100)

	// messages that have been successfully queued
	queued := make([]*models.MsgOut, 0, len(msgs))

	for _, m := range msgs {
		// ignore any message already marked as failed (maybe org is suspended)
		if m.Status() == models.MsgStatusFailed {
			queued = append(queued, m) // so that we don't try to requeue
			continue
		}

		channel := oa.ChannelByID(m.ChannelID())

		if channel != nil {
			if channel.IsAndroid() {
				androidMsgs[channel] = append(androidMsgs[channel], m)
			} else {
				cc := contactAndChannel{m.ContactID(), channel}
				courierMsgs[cc] = append(courierMsgs[cc], m)
			}
		}
	}

	// if there are courier messages to queue, do so
	if len(courierMsgs) > 0 {
		if err := flagOutsideWindow(ctx, rt, courierMsgs); err != nil {
			slog.Error("error checking customer service windows", "error", err) // messages are still sent
		}

		vc := rt.VK.Get()
		defer vc.Close()

		for cc, contactMsgs := range courierMsgs {
			err := QueueCourierMessages(vc, oa, cc.contactID, cc.channel, contactMsgs)

			// just log the error and continue to try - messages that weren't queued will be retried later
			if err != nil {
				slog.Error("error queuing messages", "error", err, "channel_uuid", cc.channel.UUID(), "contact_id", cc.contactID)
			} else {
				queued = append(queued, contactMsgs...)
			}
		}
	}

	// if we have any android messages, trigger syncs for the unique channels
	if len(androidMsgs) > 0 {
		for ch, chMsgs := range androidMsgs {
			err := SyncAndroidChannel(ctx, rt, ch)
			if err != nil {
				slog.Error("error syncing messages", "error", err, "channel_uuid", ch.UUID())
			}

			// even if syncing fails, we consider these messages queued because the device will try to sync by itself
			queued = append(queued, chMsgs...)
		}
	}

	return queued
}

// customerServiceWindow is how long after a contact's last incoming message that a WhatsApp channel can send them
// free-form messages
const customerServiceWindow = 24 * time.Hour

// flagOutsideWindow sets OutsideWindow on the flow messages to WhatsApp Cloud API style channels whose contact hasn't
// sent an incoming message on that channel within the customer service window. Only free-form (non-templated) flow
// messages which aren't replies to an incoming message are considered: a reply is always inside the window.
func flagOutsideWindow(ctx context.Context, rt *runtime.Runtime, msgs map[contactAndChannel][]*models.MsgOut) error {
	var since time.Time // only read the clock if there are candidates, as test clocks advance on every read

	candidates := make(map[models.ContactChannel][]*models.MsgOut, len(msgs))
	unknown := make([]models.ContactChannel, 0, len(msgs)) // pairs with a recent last seen on which could be from another channel

	for cc, ccMsgs := range msgs {
		if !cc.channel.IsWhatsAppCloud() {
			continue
		}

		pair := models.ContactChannel{ContactID: cc.contactID, ChannelID: cc.channel.ID()}

		for _, m := range ccMsgs {
			if m.FlowID() != models.NilFlowID && m.Templating() == nil && m.ReplyTo == nil && m.Contact != nil {
				candidates[pair] = append(candidates[pair], m)
			}
		}
		if len(candidates[pair]) == 0 {
			continue
		}
		if since.IsZero() {
			since = dates.Now().Add(-customerServiceWindow)
		}

		// last seen on is updated by messages, channel events and calls on any channel, so if it's not recent then
		// there can't be a recent incoming message on this channel, but if it is we have to check the messages
		lastSeenOn := candidates[pair][0].Contact.LastSeenOn()
		if lastSeenOn == nil || lastSeenOn.Before(since) {
			for _, m := range candidates[pair] {
				m.OutsideWindow = true
			}
		} else {
			unknown = append(unknown, pair)
		}
	}

	if len(unknown) == 0 {
		return nil
	}

	inside, err := models.GetContactChannelsWithInbound(ctx, rt.DB, unknown, since)
	if err != nil {
		return err
	}

	for _, pair := range unknown {
		if !inside[pair] {
			for _, m := range candidates[pair] {
				m.OutsideWindow = true
			}
		}
	}
	return nil
}

func fetchMissingURNs(ctx context.Context, rt *runtime.Runtime, msgs []*models.MsgOut) error {
	// get ids of missing URNs
	ids := make([]models.URNID, 0, len(msgs))
	for _, s := range msgs {
		if s.ContactURNID() != models.NilURNID && s.URN == nil {
			ids = append(ids, s.ContactURNID())
		}
	}

	cus, err := models.LoadContactURNs(ctx, rt.DB, ids)
	if err != nil {
		return fmt.Errorf("error looking up unset contact URNs: %w", err)
	}

	urnsByID := make(map[models.URNID]*models.ContactURN, len(cus))
	for _, u := range cus {
		urnsByID[u.ID] = u
	}

	for _, m := range msgs {
		if m.ContactURNID() != models.NilURNID && m.URN == nil {
			m.URN = urnsByID[m.ContactURNID()]
		}
	}

	return nil
}

func assert(c bool, m string) {
	if !c {
		panic(m)
	}
}
