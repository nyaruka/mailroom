package msg

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
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
	TicketID     models.TicketID    `json:"ticket_id"`
}

// handles a request to resend the given messages
func handleSend(ctx context.Context, rt *runtime.Runtime, r *sendRequest) (any, int, error) {
	// grab our org
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
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

	out, ch := models.CreateMsgOut(rt, oa, contact, content, models.NilTemplateID, nil, contact.Locale(oa.Env()), nil)
	event := events.NewMsgCreated(out)

	msg, err := models.NewOutgoingChatMsg(rt, oa.Org(), ch, contact, event, r.TicketID, r.UserID)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating outgoing message: %w", err)
	}

	if err := models.InsertMessages(ctx, rt.DB, []*models.Msg{msg.Msg}); err != nil {
		return nil, 0, fmt.Errorf("error inserting outgoing message: %w", err)
	}

	// if message was a ticket reply, update the ticket
	if r.TicketID != models.NilTicketID {
		if err := models.RecordTicketReply(ctx, rt.DB, oa, r.TicketID, r.UserID, time.Now()); err != nil {
			return nil, 0, fmt.Errorf("error recording ticket reply: %w", err)
		}
	}

	msgio.QueueMessages(ctx, rt, []*models.MsgOut{msg})

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
