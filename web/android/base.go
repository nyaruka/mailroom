package android

import (
	"context"
	"fmt"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

type contactAndURN struct {
	contactID  models.ContactID
	urnID      models.URNID
	urn        urns.URN
	newContact bool
}

func resolveContact(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channelID models.ChannelID, phone string) (*contactAndURN, error) {
	urn, err := urns.ParsePhone(phone, oa.ChannelByID(channelID).Country(), true, true)
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

	contact, _, created, err := models.GetOrCreateContact(ctx, rt.DB, oa, userID, []urns.URN{urn}, channelID)
	if err != nil {
		return nil, fmt.Errorf("error getting or creating contact: %w", err)
	}

	// find the URN on the contact
	var urnID models.URNID
	if cu := contact.FindURN(urn); cu != nil {
		urnID = cu.ID
	}

	return &contactAndURN{contactID: contact.ID(), urnID: urnID, urn: urn, newContact: created}, nil
}
