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

// command line tool to re-index all contacts in the database to Elastic.
//
// go install github.com/nyaruka/mailroom/cmd/mrindex; mrindex
func main() {
	cfg, err := runtime.LoadConfig()
	if err != nil {
		slog.Error("error loading config", "error", err)
		os.Exit(1)
	}

	// only output ERROR logs
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	rt, err := runtime.NewRuntime(cfg)
	if err != nil {
		fmt.Printf("error creating runtime: %s\n", err)
		os.Exit(1)
	}

	if err := rt.Start(); err != nil {
		fmt.Printf("error starting runtime: %s\n", err)
		os.Exit(1)
	}

	ctx := context.TODO()

	if err := indexAllContacts(ctx, rt); err != nil {
		rt.Stop()
		fmt.Printf("error re-indexing contacts: %s\n", err)
		os.Exit(1)
	}

	// stop flushes remaining queued items to Elastic and spool
	rt.Stop()
}

func indexAllContacts(ctx context.Context, rt *runtime.Runtime) error {
	orgIDs, err := models.GetActiveOrgIDs(ctx, rt.DB)
	if err != nil {
		return fmt.Errorf("error getting active org IDs: %w", err)
	}

	totalIndexed := 0
	totalSkipped := 0

	for _, orgID := range orgIDs {
		orgIndexed := 0
		orgSkipped := 0
		orgBatches := 0
		afterID := models.NilContactID

		fmt.Printf(" > Indexing org #%d", orgID)

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
			totalIndexed += len(flowContacts)
			orgBatches++
			afterID = contactIDs[len(contactIDs)-1]

			if orgBatches%20 == 0 {
				fmt.Print(".")
			}

			if len(contactIDs) < contactBatchSize {
				break
			}
		}

		totalSkipped += orgSkipped
		fmt.Printf(" (%d indexed, %d skipped)\n", orgIndexed, orgSkipped)
	}

	fmt.Printf("Completed indexing (%d indexed, %d skipped)\n", totalIndexed, totalSkipped)
	return nil
}
