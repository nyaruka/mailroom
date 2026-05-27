package public

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/services/airtime/dtone"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.PublicRoute(http.MethodPost, "/airtime/dtone/status/{secret}", handleDTOneStatus)
}

// dtoneStatusBody is the subset of DT One's transaction callback payload we care about. See
// https://developers.dtone.com/docs/handling-transactions-r-for-review-copy
type dtoneStatusBody struct {
	ID         int64  `json:"id"`
	ExternalID string `json:"external_id"`
	Status     struct {
		Class struct {
			ID dtone.StatusCID `json:"id"`
		} `json:"class"`
		Message string `json:"message"`
	} `json:"status"`
}

func handleDTOneStatus(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
	expected := rt.Config.DTOneCallbackSecret
	if expected == "" {
		return writeAirtimeStatusError(w, http.StatusNotFound, "dtone callbacks not enabled")
	}
	if subtle.ConstantTimeCompare([]byte(r.PathValue("secret")), []byte(expected)) != 1 {
		return writeAirtimeStatusError(w, http.StatusForbidden, "invalid secret")
	}

	body := &dtoneStatusBody{}
	if err := json.NewDecoder(r.Body).Decode(body); err != nil {
		return writeAirtimeStatusError(w, http.StatusBadRequest, fmt.Sprintf("invalid body: %s", err))
	}

	if body.ExternalID == "" {
		return writeAirtimeStatusError(w, http.StatusBadRequest, "missing external_id")
	}

	// look up the airtime transfer by its UUID — DT One echoes back the reference we passed in their
	// external_id field, which we set to the airtime_created event UUID at Create time
	transfer, err := models.GetAirtimeTransferByUUID(ctx, rt.DB, flows.EventUUID(body.ExternalID))
	if err != nil {
		return fmt.Errorf("error looking up airtime transfer: %w", err)
	}
	if transfer == nil {
		// row hasn't committed yet, or the id is unknown — return 404 so DT One retries through the race
		return writeAirtimeStatusError(w, http.StatusNotFound, "transfer not found")
	}

	newStatus, ok := mapDTOneStatus(body.Status.Class.ID)
	if !ok {
		slog.Warn("ignoring dtone callback with non-terminal status", "transfer", transfer.UUID(), "class", body.Status.Class.ID, "message", body.Status.Message)
		return web.WriteMarshalled(w, http.StatusOK, map[string]string{"status": "ignored"})
	}

	updated, err := models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), transfer.Status(), newStatus)
	if err != nil {
		return fmt.Errorf("error updating airtime transfer status: %w", err)
	}
	if !updated {
		// transition wasn't allowed from the current state (duplicate / out-of-order callback) — respond
		// 200 so the provider stops retrying
		slog.Info("ignoring out-of-order dtone callback", "transfer", transfer.UUID(), "from", transfer.Status(), "to", newStatus)
		return web.WriteMarshalled(w, http.StatusOK, map[string]string{"status": "ignored"})
	}

	return web.WriteMarshalled(w, http.StatusOK, map[string]string{"status": "ok"})
}

func mapDTOneStatus(class dtone.StatusCID) (models.AirtimeTransferStatus, bool) {
	switch class {
	case dtone.StatusCIDCompleted:
		return models.AirtimeTransferStatusSuccess, true
	case dtone.StatusCIDRejected, dtone.StatusCIDCancelled, dtone.StatusCIDDeclined:
		return models.AirtimeTransferStatusFailed, true
	case dtone.StatusCIDReversed:
		return models.AirtimeTransferStatusReversed, true
	default:
		return "", false
	}
}

func writeAirtimeStatusError(w http.ResponseWriter, status int, msg string) error {
	return web.WriteMarshalled(w, status, map[string]string{"error": msg})
}
