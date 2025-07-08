package msgio

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
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
