package android

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/stringsx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/android/message", web.JSONPayload(handleMessage))
}

// Creates a new incoming message from an Android relayer sync.
//
//	{
//	  "org_id": 1,
//	  "channel_id": 12,
//	  "phone": "+250788123123",
//	  "text": "Hello world",
//	  "received_on": "2021-01-01T12:00:00Z"
//	}
type messageRequest struct {
	OrgID      models.OrgID     `json:"org_id"       validate:"required"`
	ChannelID  models.ChannelID `json:"channel_id"   validate:"required"`
	Phone      string           `json:"phone"        validate:"required"`
	Text       string           `json:"text"         validate:"required"`
	ReceivedOn time.Time        `json:"received_on"  validate:"required"`
}

func handleMessage(ctx context.Context, rt *runtime.Runtime, r *messageRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	msgID, duplicate, err := createIncomingMsg(ctx, rt, oa, r.ChannelID, r.Phone, r.Text, r.ReceivedOn)
	if err != nil {
		return nil, 0, err
	}

	return map[string]any{"id": msgID, "duplicate": duplicate}, http.StatusOK, nil
}

// createIncomingMsg creates an incoming message reported by an Android relayer, returning the id of the new message -
// or of the existing one if the relayer has already reported it, which it does when an earlier sync wasn't acked.
func createIncomingMsg(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channelID models.ChannelID, phone, text string, receivedOn time.Time) (models.MsgID, bool, error) {
	cu, err := resolveContact(ctx, rt, oa, channelID, phone)
	if err != nil {
		return models.NilMsgID, false, fmt.Errorf("error resolving contact: %w", err)
	}

	text = dbutil.ToValidUTF8(stringsx.Truncate(text, 640))

	existingID, err := checkDuplicate(ctx, rt, text, cu.contactID, receivedOn)
	if err != nil {
		return models.NilMsgID, false, fmt.Errorf("error checking for duplicate message: %w", err)
	}
	if existingID != models.NilMsgID {
		return existingID, true, nil
	}

	m := models.NewIncomingAndroid(oa.OrgID(), channelID, cu.contactID, cu.urnID, text, receivedOn)
	if err := models.InsertMessages(ctx, rt.DB, []*models.Msg{m}); err != nil {
		return models.NilMsgID, false, fmt.Errorf("error inserting message: %w", err)
	}

	err = tasks.QueueContact(ctx, rt, oa.OrgID(), m.ContactID(), &ctasks.MsgReceived{
		ChannelID:     m.ChannelID(),
		MsgUUID:       m.UUID(),
		MsgExternalID: m.ExternalIdentifier(),
		URN:           cu.urn,
		URNID:         m.ContactURNID(),
		Text:          m.Text(),
		NewContact:    cu.newContact,
	})
	if err != nil {
		return models.NilMsgID, false, fmt.Errorf("error queueing handle task: %w", err)
	}

	return m.ID(), false, nil
}

func checkDuplicate(ctx context.Context, rt *runtime.Runtime, text string, contactID models.ContactID, sentOn time.Time) (models.MsgID, error) {
	row := rt.DB.QueryRowContext(ctx, `SELECT id FROM msgs_msg WHERE direction = 'I' AND text = $1 AND contact_id = $2 AND sent_on = $3 LIMIT 1`, text, contactID, sentOn)

	var id models.MsgID
	err := row.Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return models.NilMsgID, fmt.Errorf("error checking for duplicate message: %w", err)
	}

	return id, nil
}
