package search

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// PopulateSmartGroup calculates which members should be part of a group and populates the contacts
// for that group by performing the minimum number of inserts / deletes.
func PopulateSmartGroup(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, groupID models.GroupID, query string) (int, error) {
	err := models.UpdateGroupStatus(ctx, rt.DB, groupID, models.GroupStatusEvaluating)
	if err != nil {
		return 0, fmt.Errorf("error marking dynamic group as evaluating: %w", err)
	}

	start := time.Now()

	// we have a bit of a race with the indexer process.. we want to make sure that any contacts that changed
	// before this group was updated but after the last index are included, so if a contact was modified
	// more recently than 10 seconds ago, we wait that long before starting in populating our group
	newest, err := models.GetNewestContactModifiedOn(ctx, rt.DB, oa)
	if err != nil {
		return 0, fmt.Errorf("error getting most recent contact modified_on for org: %d: %w", oa.OrgID(), err)
	}
	if newest != nil {
		n := *newest

		// if it was more recent than 10 seconds ago, sleep until it has been 10 seconds
		if n.Add(time.Second * 10).After(start) {
			sleep := n.Add(time.Second * 10).Sub(start)
			slog.Info("sleeping before evaluating dynamic group", "sleep", sleep)
			time.Sleep(sleep)
		}
	}

	// get current set of contacts in our group
	ids, err := models.GetGroupContactIDs(ctx, rt.DB, groupID)
	if err != nil {
		return 0, fmt.Errorf("unable to look up contact ids for group: %d: %w", groupID, err)
	}
	present := make(map[models.ContactID]bool, len(ids))
	for _, i := range ids {
		present[i] = true
	}

	// calculate new set of ids
	new, err := GetContactIDsForQuery(ctx, rt, oa, nil, models.ContactStatusActive, query, -1)
	if err != nil {
		return 0, fmt.Errorf("error performing query: %s for group: %d: %w", query, groupID, err)
	}

	// find which contacts need to be added or removed
	adds := make([]models.ContactID, 0, 100)
	for _, id := range new {
		if !present[id] {
			adds = append(adds, id)
		}
		delete(present, id)
	}

	// build our list of removals
	removals := make([]models.ContactID, 0, len(present))
	for id := range present {
		removals = append(removals, id)
	}

	// first remove all the contacts
	err = models.RemoveContactsFromGroupAndCampaigns(ctx, rt.DB, oa, groupID, removals)
	if err != nil {
		return 0, fmt.Errorf("error removing contacts from group: %d: %w", groupID, err)
	}

	// then add them all
	err = models.AddContactsToGroupAndCampaigns(ctx, rt.DB, oa, groupID, adds)
	if err != nil {
		return 0, fmt.Errorf("error adding contacts to group: %d: %w", groupID, err)
	}

	// mark our group as no longer evaluating
	err = models.UpdateGroupStatus(ctx, rt.DB, groupID, models.GroupStatusReady)
	if err != nil {
		return 0, fmt.Errorf("error marking dynamic group as ready: %w", err)
	}

	// finally update modified_on for all affected contacts to ensure these changes are seen by rp-indexer
	changed := make([]models.ContactID, 0, len(adds))
	changed = append(changed, adds...)
	changed = append(changed, removals...)

	err = models.UpdateContactModifiedOn(ctx, rt.DB, changed)
	if err != nil {
		return 0, fmt.Errorf("error updating contact modified_on after group population: %w", err)
	}

	return len(new), nil
}
