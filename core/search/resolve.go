package search

import (
	"context"
	"fmt"
	"strconv"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

type Recipients struct {
	ContactIDs      []models.ContactID
	GroupIDs        []models.GroupID
	URNs            []urns.URN
	Query           string
	Exclusions      models.Exclusions
	ExcludeGroupIDs []models.GroupID
}

// ResolveRecipients resolves a set of contacts, groups, urns etc into a set of unique contacts
func ResolveRecipients(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, flow *models.Flow, recipients *Recipients, limit int) ([]models.ContactID, error) {
	idsSeen := make(map[models.ContactID]bool)

	// start by loading the explicitly listed contacts
	includeContacts, err := models.LoadContacts(ctx, rt.DB, oa, recipients.ContactIDs)
	if err != nil {
		return nil, err
	}
	for _, c := range includeContacts {
		idsSeen[c.ID()] = true
	}

	// created contacts are handled separately because they won't be indexed
	var createdContacts map[urns.URN]*models.Contact

	// resolve any raw URNs
	if len(recipients.URNs) > 0 {
		fetchedByURN, createdByURN, err := models.GetOrCreateContactsFromURNs(ctx, rt.DB, oa, userID, recipients.URNs)
		if err != nil {
			return nil, fmt.Errorf("error getting contact ids from urns: %w", err)
		}
		for _, c := range fetchedByURN {
			if !idsSeen[c.ID()] {
				includeContacts = append(includeContacts, c)
				idsSeen[c.ID()] = true
			}
		}

		createdContacts = createdByURN
	}

	includeGroups := make([]*models.Group, 0, len(recipients.GroupIDs))
	excludeGroups := make([]*models.Group, 0, len(recipients.ExcludeGroupIDs))

	for _, groupID := range recipients.GroupIDs {
		group := oa.GroupByID(groupID)
		if group != nil {
			includeGroups = append(includeGroups, group)
		}
	}
	for _, groupID := range recipients.ExcludeGroupIDs {
		group := oa.GroupByID(groupID)
		if group != nil {
			excludeGroups = append(excludeGroups, group)
		}
	}

	var matches []models.ContactID

	// if we're only including individual contacts and there are no exclusions, we can just return those contacts
	if len(includeGroups) == 0 && recipients.Query == "" && recipients.Exclusions == models.NoExclusions && len(excludeGroups) == 0 {
		matches := make([]models.ContactID, 0, len(includeContacts)+len(createdContacts))
		for _, c := range includeContacts {
			matches = append(matches, c.ID())
		}
		for _, c := range createdContacts {
			matches = append(matches, c.ID())
		}
		return matches, nil
	}

	// if we have only a query with no other inclusions/exclusions, check if it's a simple query
	// (e.g. uuid = "X" or id = N) that can be resolved directly from the database
	if len(includeContacts) == 0 && len(includeGroups) == 0 && len(recipients.URNs) == 0 &&
		recipients.Query != "" && recipients.Exclusions == models.NoExclusions && len(excludeGroups) == 0 {
		ids, err := resolveSimpleQuery(ctx, rt, oa, recipients.Query)
		if err != nil {
			return nil, fmt.Errorf("error resolving simple query: %w", err)
		}
		if ids != nil {
			if limit > 0 && len(ids) > limit {
				ids = ids[:limit]
			}
			return ids, nil
		}
	}

	if len(includeContacts) > 0 || len(includeGroups) > 0 || recipients.Query != "" {
		// reduce contacts to UUIDs
		includeContactUUIDs := make([]flows.ContactUUID, len(includeContacts))
		for i, contact := range includeContacts {
			includeContactUUIDs[i] = contact.UUID()
		}

		query, err := BuildRecipientsQuery(oa, flow, includeGroups, includeContactUUIDs, recipients.Query, recipients.Exclusions, excludeGroups)
		if err != nil {
			return nil, fmt.Errorf("error building query: %w", err)
		}

		matches, err = GetContactIDsForQuery(ctx, rt, oa, nil, models.ContactStatusActive, query, limit, false)
		if err != nil {
			return nil, fmt.Errorf("error performing contact search: %w", err)
		}
	}

	// only add created contacts if not excluding contacts based on last seen - other exclusions can't apply to a newly
	// created contact
	if recipients.Exclusions.NotSeenSinceDays == 0 {
		for _, c := range createdContacts {
			matches = append(matches, c.ID())
		}
	}

	return matches, nil
}

// resolveSimpleQuery checks if a query is a simple uuid= or id= condition that can be resolved
// directly from the database without going through ES/OS. Returns nil, nil if the query is not
// a simple resolvable query and should be handled by the normal path.
func resolveSimpleQuery(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, query string) ([]models.ContactID, error) {
	parsed, err := contactql.ParseQuery(oa.Env(), query, oa.SessionAssets())
	if err != nil {
		return nil, fmt.Errorf("error parsing query: %s: %w", query, err)
	}

	cond, isCondition := parsed.Root().(*contactql.Condition)
	if !isCondition || cond.PropertyType() != contactql.PropertyTypeAttribute || cond.Operator() != contactql.OpEqual {
		return nil, nil
	}

	switch cond.PropertyKey() {
	case contactql.AttributeUUID:
		return models.GetContactIDsFromUUIDs(ctx, rt.DB, oa.OrgID(), []flows.ContactUUID{flows.ContactUUID(cond.Value())})
	case contactql.AttributeID:
		id, err := strconv.Atoi(cond.Value())
		if err != nil {
			return nil, nil // not a valid numeric ID, fall through to normal path
		}
		return models.GetContactIDsByDBID(ctx, rt.DB, oa.OrgID(), []models.ContactID{models.ContactID(id)})
	}

	return nil, nil
}
