package android

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/nyaruka/mailroom/v26/core/android"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternetRoute(http.MethodPost, "/relayer/sync/{id:[0-9]+}", handleRelayerSync)
	web.InternetRoute(http.MethodPost, "/relayer/sync/{id:[0-9]+}/", handleRelayerSync)
}

// how far out of step with us a relayer's clock is allowed to be before we reject its request
const relayerRequestMaxAge = 15 * time.Minute

// how long a relayer can go without syncing before we bump its last_seen again - it syncs far more often than this
// and every sync would otherwise be a write to the channel
const relayerSeenInterval = 5 * time.Minute

// the most we'll read from a relayer before authenticating it - the signature is over the whole body so we can't
// truncate, and a device has no reason to send us anything near this
const relayerMaxBodyBytes = 1 << 20

// error ids the relayer app knows, which are part of the wire protocol
const (
	errorIDSignature  = 1
	errorIDOldRequest = 3
	errorIDUnclaimed  = 4
)

// Handles a sync from an Android relayer. This is the app's only channel of communication with us, and the app is
// frozen, so both the request and the response are the protocol it has always spoken:
//
//	POST /relayer/sync/{id}?ts={epoch-seconds}&signature={sig}
//
//	{"cmds": [{"cmd": "fcm", "fcm_id": "1234", "uuid": "..."}, {"cmd": "mt_sent", "msg_id": 123, "ts": 1638000000000}]}
//
// where the signature is the URL-safe base64 of HMAC-SHA256 over the raw request body, keyed by the channel's secret
// concatenated with the ts query parameter. The response tells the relayer which of its commands we processed and
// what we want it to do next:
//
//	{"cmds": [{"cmd": "ack", "p_id": 1}, {"cmd": "mt_bcast", "to": [{"phone": "+593979...", "id": 123}], "msg": "hi"}]}
//
// This handler owns the wire protocol - auth, the channel-state branches, the error responses the app knows - and
// hands the commands of an authenticated sync to core/android to process.
func handleRelayerSync(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
	relayerID, _ := strconv.Atoi(r.PathValue("id"))
	channelID := models.ChannelID(relayerID)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, relayerMaxBodyBytes))
	if err != nil {
		return fmt.Errorf("error reading request body: %w", err)
	}

	channel, err := models.GetAndroidChannel(ctx, rt.DB, channelID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("error loading channel: %w", err)
	}

	// a channel we don't have, or that's been released, tells its relayer to stop
	if channel == nil || !channel.IsActive {
		return writeSyncResponse(w, []any{map[string]any{"cmd": "rel", "relayer_id": relayerID}})
	}

	// a channel without a secret was never claimed and can't have signed anything
	if channel.Secret == "" {
		return writeSyncError(w, errorIDUnclaimed, "Can't sync unclaimed channel")
	}

	requestTS := r.URL.Query().Get("ts")
	ts, err := strconv.ParseInt(requestTS, 10, 64)
	if err != nil {
		return fmt.Errorf("unreadable ts on relayer sync: %s", requestTS)
	}
	if absDuration(time.Since(time.Unix(ts, 0))) > relayerRequestMaxAge {
		return writeSyncError(w, errorIDOldRequest, "Old Request")
	}

	if !checkRelayerSignature(channel.Secret, requestTS, body, r.URL.Query().Get("signature")) {
		return writeSyncError(w, errorIDSignature, "Invalid signature")
	}

	if channel.LastSeen == nil || time.Since(*channel.LastSeen) > relayerSeenInterval {
		if err := models.UpdateAndroidChannelSeen(ctx, rt.DB, channel.ID); err != nil {
			return err
		}
	}

	var cmds []*android.Command

	if len(body) > 0 {
		payload := &struct {
			Commands []*android.Command `json:"cmds"`
		}{}
		if err := json.Unmarshal(body, payload); err != nil {
			return fmt.Errorf("unparseable relayer sync body: %w", err)
		}

		// every valid sync starts by telling us how to reach the device
		if len(payload.Commands) < 1 || payload.Commands[0].Cmd != "fcm" {
			return writeSyncError(w, errorIDUnclaimed, "Missing FCM command")
		}

		cmds = payload.Commands
	}

	// a channel that hasn't been claimed yet gets sent its claim code so the device can display it
	if channel.OrgID == models.NilOrgID {
		if len(cmds) > 0 && cmds[0].UUID == string(channel.UUID) {
			return writeSyncResponse(w, []any{map[string]any{
				"cmd":                "reg",
				"relayer_claim_code": channel.ClaimCode,
				"relayer_secret":     channel.Secret,
				"relayer_id":         relayerID,
			}})
		}
		return writeSyncError(w, errorIDUnclaimed, "Can't sync unclaimed channel")
	}

	resp, err := android.ProcessSync(ctx, rt, channel, cmds)
	if err != nil {
		return err
	}

	return writeSyncResponse(w, resp)
}

// checkRelayerSignature verifies that a request really came from the device holding the channel's secret.
func checkRelayerSignature(secret, ts string, body []byte, signature string) bool {
	mac := hmac.New(sha256.New, []byte(secret+ts))
	mac.Write(body)
	expected := base64.URLEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

func writeSyncResponse(w http.ResponseWriter, cmds []any) error {
	if cmds == nil {
		cmds = []any{}
	}
	return web.WriteMarshalled(w, http.StatusOK, map[string]any{"cmds": cmds})
}

func writeSyncError(w http.ResponseWriter, errorID int, message string) error {
	slog.Debug("relayer sync rejected", "error_id", errorID, "error", message)

	return web.WriteMarshalled(w, http.StatusUnauthorized, map[string]any{"error_id": errorID, "error": message, "cmds": []any{}})
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
