package android

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
)

// the most messages we'll offer a relayer in a single sync. There's no limit on how many can be queued for it, but a
// relayer syncs continuously so a huge response just delays the first send - it'll come back for the rest.
const outboxLimit = 1000

// the channel event types a relayer can report - it only knows about calls, so anything else it sent would be a
// device we don't understand rather than something worth recording
var eventTypes = map[string]models.ChannelEventType{
	"mo_call": models.EventTypeIncomingCall,
	"mo_miss": models.EventTypeMissedCall,
	"mt_call": models.EventTypeOutgoingCall,
	"mt_miss": models.EventTypeMissedOutgoing,
}

// ProcessSync applies the commands from one relayer sync to the given (claimed, active) channel and returns the
// commands to send back: an ack for each command the relayer should stop resending, interleaved in the order they
// were given, then the channel's outbox.
func ProcessSync(ctx context.Context, rt *runtime.Runtime, channel *models.AndroidChannel, cmds []*Command) ([]any, error) {
	oa, err := models.GetOrgAssets(ctx, rt, channel.OrgID)
	if err != nil {
		return nil, fmt.Errorf("error loading org assets: %w", err)
	}

	// resolve the ids of every message the relayer has something to say about in one go
	msgIDs := make([]models.MsgID, 0, len(cmds))
	for _, c := range cmds {
		if c.MsgID != 0 {
			msgIDs = append(msgIDs, c.MessageID())
		}
	}
	refs, err := GetMessageRefs(ctx, rt, channel.OrgID, channel.ID, msgIDs)
	if err != nil {
		return nil, fmt.Errorf("error resolving messages to update: %w", err)
	}

	resp := make([]any, 0, len(cmds))
	batch := &StatusBatch{}
	seenCalls := make(map[string]bool, len(cmds))
	var syncEvent *models.SyncEvent
	var outboxExclude []models.MsgID

	// a command failing partway through mustn't discard the status changes we've already accepted, so the loop
	// records the failure and breaks rather than returning - the batch is applied either way below
	var loopErr error

	for _, c := range cmds {
		if c.Cmd == "" {
			continue
		}

		handled := false
		var extra map[string]any

		switch {
		// any command carrying a message id is about that message, whatever else it says
		case c.MsgID != 0:
			handled = batch.Add(refs[c.MessageID()], c.Cmd, c.Timestamp())

		case c.Cmd == "mo_sms":
			handled = true

			if c.Phone != "" && c.Msg != "" {
				msgID, _, err := CreateMessage(ctx, rt, oa, channel.ID, c.Phone, c.Msg, c.Timestamp())
				if err != nil {
					// a phone number we can't parse isn't something the relayer can fix by sending it again
					if !isInvalidURN(err) {
						loopErr = fmt.Errorf("error creating incoming message: %w", err)
					}
				} else {
					extra = map[string]any{"msg_id": msgID}
				}
			}

		case c.Cmd == "call":
			handled = true

			// the device sometimes reports calls from an 'unknown number', which we have nothing to attach to a
			// contact, and it repeats the same call across commands in one sync
			call := fmt.Sprintf("%d|%s|%s", c.TS, c.Type, c.Phone)
			eventType, validType := eventTypes[c.Type]

			if c.Phone != "" && validType && !seenCalls[call] {
				_, err := CreateEvent(ctx, rt, oa, channel.ID, c.Phone, eventType, null.Map[any]{"duration": int(c.Duration)}, c.Timestamp())
				if err != nil && !isInvalidURN(err) {
					loopErr = fmt.Errorf("error creating channel event: %w", err)
				}
				seenCalls[call] = true
			}

		case c.Cmd == "fcm":
			// this is how we reach the device to ask it to sync, so it's never acked - the relayer includes it in
			// every sync and we want the latest
			if err := updateChannelApp(ctx, rt, channel, c); err != nil {
				loopErr = err
			}

		case c.Cmd == "reset":
			if err := releaseChannel(ctx, rt, channel); err != nil {
				loopErr = err
			} else {
				handled = true
			}

		case c.Cmd == "status":
			// the device's own report of itself, always included in a sync so never acked
			syncEvent, err = recordSyncEvent(ctx, rt, oa, channel, c, len(cmds))
			if err != nil {
				loopErr = err
				break
			}
			outboxExclude = append(c.PendingMessages(), c.RetryMessages()...)

			// the channel has been moved to a different workspace than the device thinks it's in
			if c.OrgID != nil && models.OrgID(*c.OrgID) != channel.OrgID {
				resp = append(resp, map[string]any{"cmd": "claim", "org_id": channel.OrgID})
			}
		}

		if loopErr != nil {
			break
		}

		if c.PID != nil && handled {
			// echo the id back exactly as it was sent rather than re-encoding it, since the device matches its
			// pending commands against what it gave us
			ack := map[string]any{"cmd": "ack", "p_id": *c.PID}
			if extra != nil {
				ack["extra"] = extra
			}
			resp = append(resp, ack)
		}
	}

	if err := batch.Apply(ctx, rt, channel.OrgID); err != nil {
		return nil, err
	}
	if loopErr != nil {
		return nil, loopErr
	}

	outbox, err := buildOutboxCommands(ctx, rt, channel.ID, outboxExclude)
	if err != nil {
		return nil, err
	}
	resp = append(resp, outbox...)

	if syncEvent != nil {
		// acks aren't work we did for the device, so they don't count as commands we sent it
		if err := syncEvent.UpdateOutgoingCommandCount(ctx, rt.DB, len(resp)-countAcks(resp)); err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// updateChannelApp records the FCM id and UUID the device reports for itself, so that we can reach it to trigger a
// sync and so a re-installed app can re-attach itself to this channel.
func updateChannelApp(ctx context.Context, rt *runtime.Runtime, channel *models.AndroidChannel, c *Command) error {
	// the column is a real uuid type, so anything else would be rejected by the database and take every later sync
	// from this device down with it - we'd rather carry on with the uuid we already have
	uuid := assets.ChannelUUID(c.UUID)
	if uuid != "" && !uuids.Is(string(uuid)) {
		slog.Warn("ignoring unusable uuid from relayer", "channel_id", channel.ID, "uuid", uuid)
		uuid = ""
	}

	if channel.Config.GetString(models.ChannelConfigFCMID, "") == c.FCMID && (uuid == "" || uuid == channel.UUID) {
		return nil
	}

	if err := models.UpdateAndroidChannelApp(ctx, rt.DB, channel.ID, c.FCMID, uuid); err != nil {
		return err
	}

	// the next FCM nudge has to use the registration id we were just given
	if _, err := models.GetOrgAssetsWithRefresh(ctx, rt, channel.OrgID, models.RefreshChannels); err != nil {
		return fmt.Errorf("error refreshing channels: %w", err)
	}

	return nil
}

// recordSyncEvent stores what the device reported about itself, and raises or clears the incident for a device
// running an app older than the one we're distributing.
func recordSyncEvent(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channel *models.AndroidChannel, c *Command, numCmds int) (*models.SyncEvent, error) {
	device, os := clean(c.Device(), maxDeviceLen), clean(c.OS(), maxOSLen)
	if device != channel.Device || os != channel.OS {
		if err := models.UpdateAndroidChannelDevice(ctx, rt.DB, channel.ID, device, os); err != nil {
			return nil, err
		}
	}

	e := &models.SyncEvent{
		ChannelID:           channel.ID,
		PowerSource:         clean(c.PowerSource(), maxPowerSourceLen),
		PowerStatus:         clean(c.PowerStatus(), maxPowerStatusLen),
		PowerLevel:          c.PowerLevel(),
		NetworkType:         clean(c.NetworkType(), maxNetworkTypeLen),
		PendingMessageCount: len(c.PendingMessages()),
		RetryMessageCount:   len(c.RetryMessages()),

		// the fcm and status commands are in every sync, so they aren't work the device is reporting
		IncomingCommandCount: max(numCmds-2, 0),
	}
	if err := e.Insert(ctx, rt.DB); err != nil {
		return nil, err
	}

	if err := checkAppVersion(ctx, rt, oa, channel, c.AppVersion); err != nil {
		return nil, err
	}

	return e, nil
}

func checkAppVersion(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channel *models.AndroidChannel, reported string) error {
	latest, err := models.GetLatestAndroidAppVersion(ctx, rt.DB)
	if err != nil {
		return err
	}

	// we can only judge the device's version if it told us one and we have something to compare it to
	if latest == "" || reported == "" {
		return nil
	}

	if reported != latest {
		_, notifications, err := models.IncidentChannelOutdatedApp(ctx, rt.DB, oa, channel.ID)
		if err != nil {
			return fmt.Errorf("error creating outdated app incident: %w", err)
		}
		if err := models.PublishNotifications(ctx, rt, oa, notifications); err != nil {
			return fmt.Errorf("error publishing incident notifications: %w", err)
		}
	} else if err := models.EndChannelOutdatedAppIncidents(ctx, rt.DB, channel.ID); err != nil {
		return err
	}

	return nil
}

// releaseChannel handles a device asking to be reset, i.e. disconnected from this channel.
func releaseChannel(ctx context.Context, rt *runtime.Runtime, channel *models.AndroidChannel) error {
	if err := models.ReleaseAndroidChannel(ctx, rt.DB, channel); err != nil {
		return err
	}

	// the channel is no longer somewhere messages can be sent from
	if _, err := models.GetOrgAssetsWithRefresh(ctx, rt, channel.OrgID, models.RefreshChannels); err != nil {
		return fmt.Errorf("error refreshing channels: %w", err)
	}

	// interrupting sessions and failing whatever is queued for the channel is its own job
	task := &tasks.InterruptChannel{ChannelID: channel.ID}
	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, channel.OrgID, task, true); err != nil {
		return fmt.Errorf("error queuing interrupt channel task: %w", err)
	}

	return nil
}

// buildOutboxCommands turns the messages waiting for this channel into the fewest send commands that describe them,
// by grouping consecutive messages with the same text into one broadcast.
func buildOutboxCommands(ctx context.Context, rt *runtime.Runtime, channelID models.ChannelID, exclude []models.MsgID) ([]any, error) {
	msgs, err := models.GetAndroidOutbox(ctx, rt.DB, channelID, exclude, outboxLimit)
	if err != nil {
		return nil, err
	}

	cmds := make([]any, 0, 10)
	var to []map[string]any
	text := ""

	for _, m := range msgs {
		if m.Text != text && len(to) > 0 {
			cmds = append(cmds, map[string]any{"cmd": "mt_bcast", "to": to, "msg": text})
			to = nil
		}

		text = m.Text
		to = append(to, map[string]any{"phone": m.Phone, "id": m.ID})
	}

	if len(to) > 0 {
		cmds = append(cmds, map[string]any{"cmd": "mt_bcast", "to": to, "msg": text})
	}

	return cmds, nil
}

func countAcks(cmds []any) int {
	count := 0
	for _, c := range cmds {
		if m, ok := c.(map[string]any); ok && m["cmd"] == "ack" {
			count++
		}
	}
	return count
}
