package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Masterminds/semver"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/flow/migrate", web.JSONPayload(handleMigrate))
}

// Migrates a flow to the latest flow specification
//
//	{
//	  "flow": {"uuid": "468621a8-32e6-4cd2-afc1-04416f7151f0", "action_sets": [], ...},
//	  "to_version": "13.0.0"
//	}
type migrateRequest struct {
	Flow      json.RawMessage `json:"flow" validate:"required"`
	ToVersion *semver.Version `json:"to_version"`
}

func handleMigrate(ctx context.Context, rt *runtime.Runtime, r *migrateRequest) (any, int, error) {
	// do a JSON to JSON migration of the definition
	migrated, err := goflow.MigrateDefinition(rt.Config, r.Flow, r.ToVersion)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to migrate flow: %w", err)
	}

	// try to read result to check that it's valid
	_, err = goflow.ReadFlow(rt.Config, migrated)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to read migrated flow: %w", err)
	}

	return json.RawMessage(migrated), http.StatusOK, nil
}
