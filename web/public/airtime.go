package public

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/services/airtime/dtone"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.PublicRoute(http.MethodPost, "/airtime/dtone/status", handleDTOneStatus)
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

// handleDTOneStatus receives DT One's transaction status callbacks. Authentication is per-transaction
// via two unguessable identifiers DT One echoes back to us on every callback: the airtime_created event
// UUID (which we passed as DT One's external_id field) and DT One's own transaction id (which we stored
// on the row at Create time). A forged callback needs to know both for a specific transfer, not either
// one alone — 122 bits of UUID + the provider's tx id together are the capability token.
func handleDTOneStatus(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
	body := &dtoneStatusBody{}
	if err := web.ReadAndValidateJSON(r, body); err != nil {
		return writeAirtimeStatusError(w, http.StatusBadRequest, fmt.Sprintf("invalid body: %s", err))
	}

	if body.ExternalID == "" {
		return writeAirtimeStatusError(w, http.StatusBadRequest, "missing external_id")
	}
	if body.ID == 0 {
		return writeAirtimeStatusError(w, http.StatusBadRequest, "missing id")
	}

	transferUUID := flows.EventUUID(body.ExternalID)
	providerID := strconv.FormatInt(body.ID, 10)

	var newStatus models.AirtimeTransferStatus
	switch body.Status.Class.ID {
	case dtone.StatusCIDConfirmed:
		newStatus = models.AirtimeTransferStatusConfirmed
	case dtone.StatusCIDSubmitted:
		newStatus = models.AirtimeTransferStatusSubmitted
	case dtone.StatusCIDCompleted:
		newStatus = models.AirtimeTransferStatusCompleted
	case dtone.StatusCIDRejected:
		newStatus = models.AirtimeTransferStatusRejected
	case dtone.StatusCIDCancelled:
		newStatus = models.AirtimeTransferStatusCancelled
	case dtone.StatusCIDDeclined:
		newStatus = models.AirtimeTransferStatusDeclined
	case dtone.StatusCIDReversed:
		newStatus = models.AirtimeTransferStatusReversed
	default:
		// includes Created — we initiated the row in that state, no transition to apply
		slog.Warn("ignoring dtone callback with unmapped status", "transfer", transferUUID, "class", body.Status.Class.ID, "message", body.Status.Message)
		return web.WriteMarshalled(w, http.StatusOK, map[string]string{"status": "ignored"})
	}

	// compare-and-swap directly on the row by (UUID, provider tx id) — concurrent callbacks race safely
	// on a single SQL statement (no SELECT, no TOCTOU window). Rows affected of zero means the UUID is
	// unknown, the provider id doesn't match what we stored, or the row's current status doesn't admit
	// this transition (duplicate / out-of-order callback). The right reply to the provider is 2XX
	// either way so it stops retrying — the distinction isn't actionable for DT One.
	updated, err := models.UpdateAirtimeTransferStatus(ctx, rt.DB, transferUUID, providerID, newStatus)
	if err != nil {
		return fmt.Errorf("error updating airtime transfer status: %w", err)
	}
	if !updated {
		slog.Info("ignoring no-op dtone callback", "transfer", transferUUID, "to", newStatus)
		return web.WriteMarshalled(w, http.StatusOK, map[string]string{"status": "ignored"})
	}

	return web.WriteMarshalled(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeAirtimeStatusError(w http.ResponseWriter, status int, msg string) error {
	return web.WriteMarshalled(w, status, map[string]string{"error": msg})
}
