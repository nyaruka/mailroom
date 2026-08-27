package models

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// how long we cache the latest relayer app version for - it changes when someone uploads a new APK, which is rare
// enough that syncing relayers can be a few minutes behind noticing
const latestAppVersionTTL = 5 * time.Minute

var latestAppVersion struct {
	sync.Mutex

	version   string
	fetchedAt time.Time
}

// GetLatestAndroidAppVersion returns the version of the most recently uploaded relayer APK, or empty if there isn't
// one. Every syncing relayer needs this to know whether its own version is outdated, so it's cached in process.
func GetLatestAndroidAppVersion(ctx context.Context, db DBorTx) (string, error) {
	latestAppVersion.Lock()
	defer latestAppVersion.Unlock()

	if time.Since(latestAppVersion.fetchedAt) < latestAppVersionTTL {
		return latestAppVersion.version, nil
	}

	var version string
	err := db.GetContext(ctx, &version, `SELECT version FROM apks_apk WHERE apk_type = 'R' ORDER BY created_on DESC LIMIT 1`)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("error fetching latest android app version: %w", err)
	}

	latestAppVersion.version = version
	latestAppVersion.fetchedAt = time.Now()

	return version, nil
}

// FlushLatestAndroidAppVersion clears the cached app version, for tests.
func FlushLatestAndroidAppVersion() {
	latestAppVersion.Lock()
	defer latestAppVersion.Unlock()

	latestAppVersion.version = ""
	latestAppVersion.fetchedAt = time.Time{}
}
