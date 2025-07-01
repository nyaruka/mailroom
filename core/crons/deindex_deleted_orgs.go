package crons

import (
	"context"
	"fmt"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	deindexContactsSetKey  = "deindex:contacts"
	deindexDeleteBatchSize = 10000
)

func init() {
	Register("deindex_deleted_orgs", &DeindexDeletedOrgsCron{})
}

type DeindexDeletedOrgsCron struct{}

func (c *DeindexDeletedOrgsCron) Next(last time.Time) time.Time {
	return Next(last, time.Minute*5)
}

func (c *DeindexDeletedOrgsCron) AllInstances() bool {
	return false
}

func (c *DeindexDeletedOrgsCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	rc := rt.VK.Get()
	defer rc.Close()

	// get org ids that still have contacts to de-index
	orgIDs, err := redis.Ints(rc.Do("SMEMBERS", deindexContactsSetKey))
	if err != nil {
		return nil, err
	}

	contactsDeindexed := make(map[models.OrgID]int, len(orgIDs))

	for _, orgID := range orgIDs {
		deindexed, err := search.DeindexContactsByOrg(ctx, rt, models.OrgID(orgID), deindexDeleteBatchSize)
		if err != nil {
			return nil, err
		}
		contactsDeindexed[models.OrgID(orgID)] = deindexed

		if deindexed == 0 {
			if _, err := rc.Do("SREM", deindexContactsSetKey, orgID); err != nil {
				return nil, fmt.Errorf("error removing org #%d from deindex set: %w", orgID, err)
			}
		}
	}

	return map[string]any{"contacts": contactsDeindexed}, nil
}

// MarkOrgForDeindexing marks the given org for de-indexing
func MarkOrgForDeindexing(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID) error {
	rc := rt.VK.Get()
	defer rc.Close()

	if _, err := rc.Do("SADD", deindexContactsSetKey, orgID); err != nil {
		return fmt.Errorf("error adding org #%d to deindex set: %w", orgID, err)
	}

	return nil
}
