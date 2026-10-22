package ctasks

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const TypeTicketInactive = "ticket_inactive"

func init() {
	RegisterType(TypeTicketInactive, func() Task { return &TicketInactive{} })
}

// TicketInactive is queued when a ticket's close fire is due, and closes the ticket if it's still inactive
type TicketInactive struct {
	TicketUUID core.TicketUUID `json:"ticket_uuid" validate:"required"`
}

func (t *TicketInactive) Type() string {
	return TypeTicketInactive
}

func (t *TicketInactive) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	log := slog.With("ctask", "ticket_inactive", "contact_id", mc.ID(), "ticket_uuid", t.TicketUUID)

	// contacts are only loaded with their open tickets
	ticket := mc.FindTicket(t.TicketUUID)
	if ticket == nil {
		log.Debug("skipping as ticket is no longer open")
		return nil
	}

	idleDays := oa.Org().MaxTicketIdleDays()
	if idleDays <= 0 {
		log.Debug("skipping as workspace no longer closes inactive tickets")
		return nil
	}

	// if there's been activity since the fire was scheduled, or the limit has been raised, reschedule it
	closeOn := ticket.LastActivityOn.Add(time.Duration(idleDays) * 24 * time.Hour)
	if closeOn.After(dates.Now()) {
		fire := models.NewContactFireForTicketClose(oa.OrgID(), mc.ID(), ticket.UUID, closeOn.Add(time.Hour))
		if err := models.InsertContactFires(ctx, rt.DB, []*models.ContactFire{fire}); err != nil {
			return fmt.Errorf("error rescheduling ticket close fire: %w", err)
		}
		return nil
	}

	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating engine contact: %w", err)
	}

	scene := runner.NewScene(mc, contact)
	if err := scene.ApplyModifier(ctx, rt, oa, modifiers.NewTicketClose(ticket.UUID), models.NilUserID, ""); err != nil {
		return fmt.Errorf("error closing ticket: %w", err)
	}

	return scene.Commit(ctx, rt, oa)
}
