package clocks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/vkutil/locks"
)

// TryToLock tries to grab locks for the given contacts, returning the locks and the skipped contacts
func TryToLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ids []models.ContactID, retry time.Duration) (map[models.ContactID]string, []models.ContactID, error) {
	values := make(map[models.ContactID]string, len(ids))
	skipped := make([]models.ContactID, 0, 5)

	// this is set to true at the end of the function so the defer calls won't release the locks unless we're returning
	// early due to an error
	success := false

	for _, contactID := range ids {
		locker := getContactLocker(oa.OrgID(), contactID)

		lock, err := locker.Grab(ctx, rt.VK, retry)
		if err != nil {
			return nil, nil, fmt.Errorf("error attempting to grab lock: %w", err)
		}

		// no error but we didn't get the lock
		if lock == "" {
			skipped = append(skipped, contactID)
			continue
		}

		values[contactID] = lock

		// if we error we want to release all locks on way out
		defer func() {
			if !success {
				locker.Release(context.Background(), rt.VK, lock)
			}
		}()
	}

	success = true
	return values, skipped, nil
}

// Unlock unlocks the given contacts using the given lock values
func Unlock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, locks map[models.ContactID]string) error {
	for contactID, lock := range locks {
		locker := getContactLocker(oa.OrgID(), contactID)

		err := locker.Release(ctx, rt.VK, lock)
		if err != nil {
			return err
		}
	}
	return nil
}

func IsLocked(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactID models.ContactID) (bool, error) {
	locker := getContactLocker(oa.OrgID(), contactID)

	locked, err := locker.IsLocked(ctx, rt.VK)
	if err != nil {
		return false, fmt.Errorf("error checking if contact locked: %w", err)
	}

	return locked, nil
}

// returns the locker for a particular contact
func getContactLocker(orgID models.OrgID, contactID models.ContactID) *locks.Locker {
	return locks.NewLocker(fmt.Sprintf("lock:c:%d:%d", orgID, contactID), time.Minute*5)
}
