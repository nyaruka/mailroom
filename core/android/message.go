package android

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/stringsx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// CreateMessage creates an incoming message reported by an Android relayer, returning the id of the new message - or
// of the existing one if the relayer has already reported it, which it does when an earlier sync wasn't acked.
func CreateMessage(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channelID models.ChannelID, phone, text string, receivedOn time.Time) (models.MsgID, bool, error) {
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

type contactAndURN struct {
	contactID  models.ContactID
	urnID      models.URNID
	urn        urns.URN
	newContact bool
}

func resolveContact(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channelID models.ChannelID, phone string) (*contactAndURN, error) {
	// a channel that's been disabled or released is still one an Android relayer can be syncing
	channel := oa.ChannelByID(channelID)
	if channel == nil {
		return nil, fmt.Errorf("no active channel with id %d", channelID)
	}

	urn, err := urns.ParsePhone(phone, channel.Country(), true, true)
	if err != nil {
		return nil, models.NewURNInvalidError(0, err)
	}

	if err := urn.Validate(); err != nil {
		return nil, fmt.Errorf("URN failed validation: %w", err)
	}

	userID, err := models.GetSystemUserID(ctx, rt.DB.DB)
	if err != nil {
		return nil, fmt.Errorf("error getting system user id: %w", err)
	}

	mc, _, created, err := models.GetOrCreateContact(ctx, rt.DB, oa, userID, []urns.URN{urn}, channelID)
	if err != nil {
		return nil, fmt.Errorf("error getting or creating contact: %w", err)
	}

	// find the URN on the contact
	var urnID models.URNID
	if cu := mc.FindURN(urn); cu != nil {
		urnID = cu.ID
	}

	return &contactAndURN{contactID: mc.ID(), urnID: urnID, urn: urn, newContact: created}, nil
}

// isInvalidURN reports whether the error is the device having given us a phone number we can't make sense of.
func isInvalidURN(err error) bool {
	var urnErr *models.URNError
	return errors.As(err, &urnErr) && urnErr.Code == "invalid"
}
