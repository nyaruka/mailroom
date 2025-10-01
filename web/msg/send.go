package msg

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/msg/send", web.JSONPayload(handleSend))
}

// Request to send a message.
//
//	{
//	  "org_id": 1,
//	  "contact_id": 123456,
//	  "user_id": 56,
//	  "text": "hi there"
//	}
type sendRequest struct {
	OrgID        models.OrgID       `json:"org_id"       validate:"required"`
	UserID       models.UserID      `json:"user_id"      validate:"required"`
	ContactID    models.ContactID   `json:"contact_id"   validate:"required"`
	Text         string             `json:"text"`
	Attachments  []utils.Attachment `json:"attachments"`
	QuickReplies []flows.QuickReply `json:"quick_replies"`
	TicketUUID   flows.TicketUUID   `json:"ticket_uuid"`
}

// handles a request to resend the given messages
func handleSend(ctx context.Context, rt *runtime.Runtime, r *sendRequest) (any, int, error) {
	// grab our org
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	// load the contact and generate as a flow contact
	c, err := models.LoadContact(ctx, rt.DB, oa, r.ContactID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading contact: %w", err)
	}

	contact, err := c.EngineContact(oa)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating flow contact: %w", err)
	}

	content := &flows.MsgContent{Text: r.Text, Attachments: r.Attachments, QuickReplies: r.QuickReplies}
	out, err := models.CreateMsgOut(rt, oa, contact, content, models.NilTemplateID, nil, contact.Locale(oa.Env()), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating message content: %w", err)
	}

	event := events.NewMsgCreated(out, "", r.TicketUUID)

	scene := runner.NewScene(c, contact)

	if err := scene.AddEvent(ctx, rt, oa, event, r.UserID); err != nil {
		return nil, 0, fmt.Errorf("error adding message event to scene: %w", err)
	}
	if err := scene.Commit(ctx, rt, oa); err != nil {
		return nil, 0, fmt.Errorf("error committing scene: %w", err)
	}

	msg := scene.SentMsgs[0]

	// TODO move this into event handler?
	// if message was a ticket reply, update the ticket
	if r.TicketUUID != "" {
		if err := models.RecordTicketReply(ctx, rt.DB, oa, r.TicketUUID, r.UserID, dates.Now()); err != nil {
			return nil, 0, fmt.Errorf("error recording ticket reply: %w", err)
		}
	}

	return map[string]any{
		"id":            msg.ID(),
		"channel":       out.Channel(),
		"contact":       contact.Reference(),
		"urn":           out.URN(),
		"text":          msg.Text(),
		"attachments":   msg.Attachments(),
		"quick_replies": msg.QuickReplies(),
		"status":        msg.Status(),
		"created_on":    msg.CreatedOn(),
		"modified_on":   msg.ModifiedOn(),
	}, http.StatusOK, nil
}
