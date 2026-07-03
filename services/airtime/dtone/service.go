package dtone

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/stringsx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/flows"
	"github.com/shopspring/decimal"
)

type service struct {
	client      *Client
	redactor    stringsx.Redactor
	callbackURL string
}

// NewService creates a new DTOne airtime service. The callbackURL is the URL DT One will POST to with
// transaction status updates. Authentication of callbacks is per-transaction via the row UUID we send
// as DT One's external_id plus the tx id they return — the URL itself doesn't carry a secret.
func NewService(httpClient *http.Client, httpRetries *httpx.RetryConfig, key, secret, callbackURL string) flows.AirtimeService {
	return &service{
		client:      NewClient(httpClient, httpRetries, key, secret),
		redactor:    stringsx.NewRedactor(core.RedactionMask, secret),
		callbackURL: callbackURL,
	}
}

// Create resolves the operator + product for the recipient and submits an unconfirmed transaction to DT One.
// The transferUUID is passed to DT One as our reference (their `external_id` request field), so subsequent
// status callbacks echo it back and the host can correlate them to the airtime_created event by UUID.
// The returned ExternalID is DT One's transaction id; nothing is actually sent until the host calls Confirm.
func (s *service) Create(ctx context.Context, transferUUID events.EventUUID, sender urns.URN, recipient urns.URN, amounts map[string]decimal.Decimal, logHTTP core.HTTPLogCallback) (*core.AirtimeTransfer, error) {
	transfer := &core.AirtimeTransfer{
		Sender:    sender,
		Recipient: recipient,
		Currency:  "",
		Amount:    decimal.Zero,
	}

	recipientPhone := recipient.Path()
	if !strings.HasPrefix(recipientPhone, "+") {
		recipientPhone = "+" + recipientPhone
	}

	operators, trace, err := s.client.LookupMobileNumber(ctx, recipientPhone)
	if trace != nil {
		logHTTP(core.NewHTTPLog(trace, core.HTTPStatusFromCode, s.redactor))
	}
	if err != nil {
		return transfer, fmt.Errorf("number lookup failed: %w", err)
	}

	var operator *Operator
	for _, op := range operators {
		if op.Identified {
			operator = op
			break
		}
	}
	if operator == nil {
		return transfer, fmt.Errorf("unable to find operator for number %s", recipientPhone)
	}

	products, trace, err := s.client.Products(ctx, "FIXED_VALUE_RECHARGE", operator.ID)
	if trace != nil {
		logHTTP(core.NewHTTPLog(trace, core.HTTPStatusFromCode, s.redactor))
	}
	if err != nil {
		return transfer, fmt.Errorf("product fetch failed: %w", err)
	}

	var product *Product
	for currency, desiredAmount := range amounts {
		for _, p := range products {
			if p.Destination.Unit == currency && p.Destination.Amount.Equal(desiredAmount) {
				product = p
				break
			}
		}
		if product != nil {
			break
		}
	}
	if product == nil {
		return transfer, fmt.Errorf("unable to find a suitable product for operator '%s'", operator.Name)
	}

	transfer.Currency = product.Destination.Unit
	transfer.Amount = product.Destination.Amount

	// submit the transaction in a held state; the host triggers actual delivery via Confirm after commit
	tx, trace, err := s.client.TransactionAsync(ctx, string(transferUUID), product.ID, recipientPhone, s.callbackURL)
	if trace != nil {
		logHTTP(core.NewHTTPLog(trace, core.HTTPStatusFromCode, s.redactor))
	}
	if err != nil {
		return transfer, fmt.Errorf("transaction creation failed: %w", err)
	}

	// auto_confirm: false means the only success status we should ever see here is Created (held). Anything
	// else — including Confirmed/Submitted/Completed (the host hasn't confirmed yet, so the provider can't
	// have advanced past Created) or the terminal failure classes (Rejected/Declined/Cancelled) — indicates
	// the transaction will not be confirmable by the host's later Confirm call, so we fail Create here
	// rather than commit a row that's destined to get stuck.
	if tx.Status.Class.ID != StatusCIDCreated {
		return transfer, fmt.Errorf("transaction rejected by provider with status %s", tx.Status.Message)
	}

	transfer.ExternalID = strconv.FormatInt(tx.ID, 10)
	return transfer, nil
}

// Confirm triggers DT One to actually send the airtime for the previously-created transaction. The provider
// transaction id is read from transfer.ExternalID, which Create set on initiation.
func (s *service) Confirm(ctx context.Context, transfer *core.AirtimeTransfer, logHTTP core.HTTPLogCallback) error {
	txID, err := strconv.ParseInt(transfer.ExternalID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid transaction id %q: %w", transfer.ExternalID, err)
	}

	_, trace, err := s.client.ConfirmTransaction(ctx, txID)
	if trace != nil {
		logHTTP(core.NewHTTPLog(trace, core.HTTPStatusFromCode, s.redactor))
	}
	if err != nil {
		return fmt.Errorf("transaction confirmation failed: %w", err)
	}
	return nil
}
