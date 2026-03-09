package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/lib/pq"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
)

const contactBatchSize = 500

// command line tool to re-index all contacts in the database to OpenSearch.
//
// go install github.com/nyaruka/mailroom/cmd/mrindex; mrindex
func main() {
	cfg, err := runtime.LoadConfig()
	if err != nil {
		slog.Error("error loading config", "error", err)
		os.Exit(1)
	}

	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})
	slog.SetDefault(slog.New(logHandler))

	log := slog.With("comp", "mrindex")
	log.Info("starting contact re-indexing")

	rt, err := runtime.NewRuntime(cfg)
	if err != nil {
		log.Error("error creating runtime", "error", err)
		os.Exit(1)
	}

	if err := rt.Start(); err != nil {
		log.Error("error starting runtime", "error", err)
		os.Exit(1)
	}

	ctx := context.TODO()

	if err := indexAllContacts(ctx, rt); err != nil {
		rt.Stop()
		log.Error("error re-indexing contacts", "error", err)
		os.Exit(1)
	}

	// stop flushes remaining queued items to OpenSearch and spool
	rt.Stop()

	log.Info("contact re-indexing complete")
}

func indexAllContacts(ctx context.Context, rt *runtime.Runtime) error {
	if rt.Config.OSContactsIndex == "" {
		return fmt.Errorf("OSContactsIndex not configured")
	}

	log := slog.With("comp", "mrindex")

	orgIDs, err := models.GetActiveOrgIDs(ctx, rt.DB)
	if err != nil {
		return fmt.Errorf("error getting active org IDs: %w", err)
	}

	log.Info("found active orgs", "count", len(orgIDs))

	totalIndexed := 0
	totalSkipped := 0

	for i, orgID := range orgIDs {
		orgIndexed := 0
		orgSkipped := 0
		afterID := models.NilContactID

		for {
			contactIDs, err := models.GetContactIDsPage(ctx, rt.DB, orgID, afterID, contactBatchSize)
			if err != nil {
				return fmt.Errorf("error getting contact IDs for org #%d: %w", orgID, err)
			}

			if len(contactIDs) == 0 {
				break
			}

			// get org assets (cached but periodically refreshed for large orgs)
			oa, err := models.GetOrgAssets(ctx, rt, orgID)
			if err != nil {
				return fmt.Errorf("error loading org assets for org #%d: %w", orgID, err)
			}

			contacts, err := models.LoadContacts(ctx, rt.DB, oa, contactIDs)
			if err != nil {
				return fmt.Errorf("error loading contacts for org #%d: %w", orgID, err)
			}

			flowContacts := make([]*flows.Contact, 0, len(contacts))
			currentFlows := make(map[models.ContactID]models.FlowID, len(contacts))
			for _, c := range contacts {
				fc, err := c.EngineContact(oa)
				if err != nil {
					log.Warn("error creating flow contact, skipping", "org_id", orgID, "contact_id", c.ID(), "error", err)
					orgSkipped++
					continue
				}
				flowContacts = append(flowContacts, fc)
				currentFlows[c.ID()] = c.CurrentFlowID()
			}

			if err := search.IndexContacts(ctx, rt, oa, flowContacts, currentFlows); err != nil {
				return fmt.Errorf("error indexing contacts for org #%d: %w", orgID, err)
			}

			orgIndexed += len(flowContacts)
			afterID = contactIDs[len(contactIDs)-1]

			if len(contactIDs) < contactBatchSize {
				break
			}
		}

		totalIndexed += orgIndexed
		totalSkipped += orgSkipped
		log.Info("indexed org contacts", "org_id", orgID, "org_num", i+1, "org_total", len(orgIDs), "org_indexed", orgIndexed, "org_skipped", orgSkipped, "total_indexed", totalIndexed)
	}

	log.Info("re-indexing complete", "total_indexed", totalIndexed, "total_skipped", totalSkipped, "orgs", len(orgIDs))
	return nil
}
