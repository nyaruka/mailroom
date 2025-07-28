package android

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/stringsx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/android/message", web.RequireAuthToken(web.JSONPayload(handleMessage)))
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
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	cu, err := resolveContact(ctx, rt, oa, r.ChannelID, r.Phone)
	if err != nil {
		return nil, 0, fmt.Errorf("error resolving contact: %w", err)
	}

	text := dbutil.ToValidUTF8(stringsx.Truncate(r.Text, 640))

	existingID, err := checkDuplicate(ctx, rt, text, cu.contactID, r.ReceivedOn)
	if err != nil {
		return nil, 0, fmt.Errorf("error checking for duplicate message: %w", err)
	}
	if existingID != models.NilMsgID {
		return map[string]any{"id": existingID, "duplicate": true}, http.StatusOK, nil
	}

	m := models.NewIncomingAndroid(r.OrgID, r.ChannelID, cu.contactID, cu.urnID, text, r.ReceivedOn)
	if err := models.InsertMessages(ctx, rt.DB, []*models.Msg{m}); err != nil {
		return nil, 0, fmt.Errorf("error inserting message: %w", err)
	}

	err = realtime.QueueTask(ctx, rt, r.OrgID, m.ContactID(), &ctasks.MsgReceivedTask{
		ChannelID:     m.ChannelID(),
		MsgID:         m.ID(),
		MsgUUID:       m.UUID(),
		MsgExternalID: m.ExternalID(),
		URN:           cu.urn,
		URNID:         m.ContactURNID(),
		Text:          m.Text(),
		NewContact:    cu.newContact,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("error queueing handle task: %w", err)
	}

	return map[string]any{"id": m.ID(), "duplicate": false}, http.StatusOK, nil
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
