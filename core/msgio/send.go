package msgio

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

type Send struct {
	Msg *models.Msg
	URN *models.ContactURN
}

type contactAndChannel struct {
	contactID models.ContactID
	channel   *models.Channel
}

// QueueMessages tries to queue the given messages to courier or trigger Android channel syncs
func QueueMessages(ctx context.Context, rt *runtime.Runtime, sends []*Send) {
	queued := tryToQueue(ctx, rt, sends)

	if len(queued) != len(sends) {
		retry := make([]*models.Msg, 0, len(sends)-len(queued))
		for _, s := range sends {
			if !slices.Contains(queued, s) {
				retry = append(retry, s.Msg)
			}
		}

		// any messages that failed to queue should be moved back to initializing(I) (they are queued(Q) at creation to
		// save an update in the common case)
		if err := models.MarkMessagesForRequeuing(ctx, rt.DB, retry); err != nil {
			slog.Error("error marking messages as initializing", "error", err)
		}
	}
}

func tryToQueue(ctx context.Context, rt *runtime.Runtime, sends []*Send) []*Send {
	if err := fetchMissingURNs(ctx, rt, sends); err != nil {
		slog.Error("error fetching missing contact URNs", "error", err)
		return nil
	}

	// messages that have been successfully queued
	queued := make([]*Send, 0, len(sends))

	// organize what we have to send by org
	sendsByOrg := make(map[models.OrgID][]*Send)
	for _, s := range sends {
		orgID := s.Msg.OrgID()
		sendsByOrg[orgID] = append(sendsByOrg[orgID], s)
	}

	for orgID, orgSends := range sendsByOrg {
		oa, err := models.GetOrgAssets(ctx, rt, orgID)
		if err != nil {
			slog.Error("error getting org assets", "error", err)
		} else {
			queued = append(queued, tryToQueueForOrg(ctx, rt, oa, orgSends)...)
		}
	}

	return queued
}

func tryToQueueForOrg(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, sends []*Send) []*Send {
	// sends by courier, organized by contact+channel
	courierSends := make(map[contactAndChannel][]*Send, 100)

	// android channels that need to be notified to sync
	androidMsgs := make(map[*models.Channel][]*Send, 100)

	// messages that have been successfully queued
	queued := make([]*Send, 0, len(sends))

	for _, s := range sends {
		// ignore any message already marked as failed (maybe org is suspended)
		if s.Msg.Status() == models.MsgStatusFailed {
			queued = append(queued, s) // so that we don't try to requeue
			continue
		}

		channel := oa.ChannelByID(s.Msg.ChannelID())

		if channel != nil {
			if channel.IsAndroid() {
				androidMsgs[channel] = append(androidMsgs[channel], s)
			} else {
				cc := contactAndChannel{s.Msg.ContactID(), channel}
				courierSends[cc] = append(courierSends[cc], s)
			}
		}
	}

	// if there are courier messages to queue, do so
	if len(courierSends) > 0 {
		rc := rt.RP.Get()
		defer rc.Close()

		for cc, contactSends := range courierSends {
			err := QueueCourierMessages(rc, oa, cc.contactID, cc.channel, contactSends)

			// just log the error and continue to try - messages that weren't queued will be retried later
			if err != nil {
				slog.Error("error queuing messages", "error", err, "channel_uuid", cc.channel.UUID(), "contact_id", cc.contactID)
			} else {
				queued = append(queued, contactSends...)
			}
		}
	}

	// if we have any android messages, trigger syncs for the unique channels
	if len(androidMsgs) > 0 {
		for ch, chSends := range androidMsgs {
			err := SyncAndroidChannel(ctx, rt, ch)
			if err != nil {
				slog.Error("error syncing messages", "error", err, "channel_uuid", ch.UUID())
			}

			// even if syncing fails, we consider these messages queued because the device will try to sync by itself
			queued = append(queued, chSends...)
		}
	}

	return queued
}

func fetchMissingURNs(ctx context.Context, rt *runtime.Runtime, sends []*Send) error {
	// get ids of missing URNs
	ids := make([]models.URNID, 0, len(sends))
	for _, s := range sends {
		if s.Msg.ContactURNID() != models.NilURNID && s.URN == nil {
			ids = append(ids, s.Msg.ContactURNID())
		}
	}

	urns, err := models.LoadContactURNs(ctx, rt.DB, ids)
	if err != nil {
		return fmt.Errorf("error looking up unset contact URNs: %w", err)
	}

	urnsByID := make(map[models.URNID]*models.ContactURN, len(urns))
	for _, u := range urns {
		urnsByID[u.ID] = u
	}

	for _, s := range sends {
		if s.Msg.ContactURNID() != models.NilURNID && s.URN == nil {
			s.URN = urnsByID[s.Msg.ContactURNID()]
		}
	}

	return nil
}

func assert(c bool, m string) {
	if !c {
		panic(m)
	}
}
