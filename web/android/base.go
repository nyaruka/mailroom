package android

import (
	"context"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/pkg/errors"
)

type contactAndURN struct {
	contactID  models.ContactID
	urnID      models.URNID
	urn        urns.URN
	newContact bool
}

func resolveContact(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channelID models.ChannelID, urn urns.URN) (*contactAndURN, error) {
	urn = urn.Normalize(string(oa.Env().DefaultCountry()))

	// TODO rework normalization to be idempotent because an invalid number like +2621112222 normalizes to
	// 2621112222 (invalid) and then normalizes to +12621112222 (valid)
	urn = urn.Normalize(string(oa.Env().DefaultCountry()))

	if err := urn.Validate(); err != nil {
		return nil, errors.Wrap(err, "URN failed validation")
	}

	contact, flowContact, created, err := models.GetOrCreateContact(ctx, rt.DB, oa, []urns.URN{urn}, channelID)
	if err != nil {
		return nil, errors.Wrap(err, "error getting or creating contact")
	}

	// find the URN on the contact
	for _, u := range flowContact.URNs() {
		if urn.Identity() == u.URN().Identity() {
			urn = u.URN()
			break
		}
	}
	urnID := models.URNID(models.GetURNInt(urn, "id"))

	return &contactAndURN{contactID: contact.ID(), urnID: urnID, urn: urn, newContact: created}, nil
}