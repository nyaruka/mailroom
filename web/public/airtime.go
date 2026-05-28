package public

import (
	"context"
	"crypto/subtle"
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
	if err := web.ReadAndValidateJSON(r, body); err != nil {
		return writeAirtimeStatusError(w, http.StatusBadRequest, fmt.Sprintf("invalid body: %s", err))
	}

	if body.ExternalID == "" {
		return writeAirtimeStatusError(w, http.StatusBadRequest, "missing external_id")
	}

	transferUUID := flows.EventUUID(body.ExternalID)

	newStatus, ok := mapDTOneStatus(body.Status.Class.ID)
	if !ok {
		slog.Warn("ignoring dtone callback with non-terminal status", "transfer", transferUUID, "class", body.Status.Class.ID, "message", body.Status.Message)
		return web.WriteMarshalled(w, http.StatusOK, map[string]string{"status": "ignored"})
	}

	// compare-and-swap directly on the row by UUID — concurrent callbacks race safely on a single SQL
	// statement (no SELECT, no TOCTOU window). Rows affected of zero means either: the UUID is unknown,
	// or the row's current status doesn't admit this transition (duplicate / out-of-order callback).
	// Either way the right reply to the provider is 2XX so it stops retrying — we can't distinguish
	// the two cases without an extra SELECT and the distinction isn't actionable for DT One.
	updated, err := models.UpdateAirtimeTransferStatus(ctx, rt.DB, transferUUID, newStatus)
	if err != nil {
		return fmt.Errorf("error updating airtime transfer status: %w", err)
	}
	if !updated {
		slog.Info("ignoring no-op dtone callback", "transfer", transferUUID, "to", newStatus)
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
