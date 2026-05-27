package public

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

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

	if body.ID == 0 {
		return writeAirtimeStatusError(w, http.StatusBadRequest, "missing transaction id")
	}

	// look up the airtime transfer using DT One's transaction id, which we store as external_id on the row
	externalID := strconv.FormatInt(body.ID, 10)
	transfer, err := models.GetAirtimeTransferByExternalID(ctx, rt.DB, externalID)
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

	if err := models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), newStatus, externalID); err != nil {
		return fmt.Errorf("error updating airtime transfer status: %w", err)
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
