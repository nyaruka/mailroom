package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
)

const indexBatchSize = 500

const elasticUsage = "usage: mrelastic [flags] <verb> <target> where valid combinations are 'index contacts', 'index messages' and 'prune contacts'"

// Elastic is the entry point for the mrelastic command which manages the search indexes. Configuration is
// loaded on top of the given defaults, e.g. runtime.NewDefaultConfig().
func Elastic(defaults *runtime.Config) error {
	// our own flags have to come out of the command line before the rest of it is loaded as configuration
	flags := flag.NewFlagSet("mrelastic", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // we report parse errors ourselves rather than having them printed with usage
	del := flags.Bool("delete", false, "delete orphaned documents instead of just reporting them (prune contacts only)")

	cmdArgs, cfgArgs, positional := runtime.SplitArgs(flags, os.Args[1:])

	if err := flags.Parse(cmdArgs); err != nil {
		return err
	}

	// the config loader shows usage for the config flags, so we show usage for ours just before it does
	if slices.Contains(cfgArgs, "-h") || slices.Contains(cfgArgs, "-help") || slices.Contains(cfgArgs, "--help") {
		fmt.Fprintln(os.Stderr, elasticUsage)
		flags.SetOutput(os.Stderr)
		flags.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}

	cfg, err := runtime.LoadConfig(defaults, cfgArgs)
	if err != nil {
		return err
	}

	// only output ERROR logs
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	var verb, target string
	if len(positional) > 0 {
		verb = positional[0]
	}
	if len(positional) > 1 {
		target = positional[1]
	}

	valid := (verb == "index" && (target == "contacts" || target == "messages")) || (verb == "prune" && target == "contacts")
	if !valid {
		return errors.New(elasticUsage)
	}

	rt, err := runtime.NewRuntime(cfg)
	if err != nil {
		return fmt.Errorf("error creating runtime: %w", err)
	}

	if err := rt.Start(); err != nil {
		return fmt.Errorf("error starting runtime: %w", err)
	}
	defer rt.Stop()

	models.InitCache(rt)

	ctx := context.TODO()

	switch verb {
	case "index":
		if target == "contacts" {
			return indexAllContacts(ctx, rt)
		}
		return indexAllMessages(ctx, rt)
	case "prune":
		return pruneAllContacts(ctx, rt, *del)
	}
	return nil
}

func pruneAllContacts(ctx context.Context, rt *runtime.Runtime, del bool) error {
	counts, err := search.PruneContacts(ctx, rt, del, func(c search.PruneCounts) {
		fmt.Printf(" > scanned %d docs (%d orphans found, %d deleted)\n", c.Scanned, c.Orphaned, c.Deleted)
	})
	if err != nil {
		return err
	}

	fmt.Printf("Completed prune (%d docs scanned, %d orphans found, %d deleted)\n", counts.Scanned, counts.Orphaned, counts.Deleted)
	if !del && counts.Orphaned > 0 {
		fmt.Println("Re-run with --delete to delete orphaned documents.")
	}
	return nil
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
			contactIDs, err := models.GetContactIDsPage(ctx, rt.DB, orgID, afterID, indexBatchSize)
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

			mcs, err := models.LoadContacts(ctx, rt.DB, oa, contactIDs)
			if err != nil {
				return fmt.Errorf("error loading contacts for org #%d: %w", orgID, err)
			}

			contacts := make([]*core.Contact, 0, len(mcs))
			currentFlows := make(map[models.ContactID]models.FlowID, len(mcs))
			for _, mc := range mcs {
				contact, err := mc.EngineContact(oa)
				if err != nil {
					orgSkipped++
					continue
				}
				contacts = append(contacts, contact)
				currentFlows[mc.ID()] = mc.CurrentFlowID()
			}

			if err := search.IndexContacts(ctx, rt, oa, contacts, currentFlows); err != nil {
				return fmt.Errorf("error indexing contacts for org #%d: %w", orgID, err)
			}

			orgIndexed += len(contacts)
			totalIndexed += len(contacts)
			orgBatches++
			afterID = contactIDs[len(contactIDs)-1]

			if orgBatches%20 == 0 {
				fmt.Print(".")
			}

			if len(contactIDs) < indexBatchSize {
				break
			}
		}

		totalSkipped += orgSkipped
		fmt.Printf(" (%d indexed, %d skipped)\n", orgIndexed, orgSkipped)
	}

	fmt.Printf("Completed indexing (%d indexed, %d skipped)\n", totalIndexed, totalSkipped)
	return nil
}

const sqlSelectMessagesForSearch = `
SELECT m.uuid, m.org_id, m.text, m.created_on, m.ticket_uuid, c.uuid AS contact_uuid, COALESCE(u.path, '') AS urn_path
  FROM msgs_msg m
  JOIN contacts_contact c ON c.id = m.contact_id
  LEFT JOIN contacts_contacturn u ON u.id = m.contact_urn_id
 WHERE c.last_seen_on IS NOT NULL
   AND LENGTH(m.text) >= $3
   AND m.visibility NOT IN ('D', 'X')
   AND m.uuid < $1
 ORDER BY m.uuid DESC
 LIMIT $2`

func indexAllMessages(ctx context.Context, rt *runtime.Runtime) error {
	// messages are indexed newest first, so we start above the highest possible UUID and work backwards
	startUUID := "ffffffff-ffff-ffff-ffff-ffffffffffff"

	numIndexed := 0
	lastUUID := ""

	for {
		rows, err := rt.DB.QueryContext(ctx, sqlSelectMessagesForSearch, startUUID, indexBatchSize, search.MessageTextMinLength)
		if err != nil {
			return fmt.Errorf("error querying messages: %w", err)
		}

		batchCount := 0
		var lastCreatedOn time.Time

		for rows.Next() {
			var msgUUID, contactUUID, urnPath string
			var orgID models.OrgID
			var text string
			var createdOn time.Time
			var ticketUUID null.String

			if err := rows.Scan(&msgUUID, &orgID, &text, &createdOn, &ticketUUID, &contactUUID, &urnPath); err != nil {
				rows.Close()
				return fmt.Errorf("error scanning message row: %w", err)
			}

			msg := &search.MessageDoc{
				CreatedOn:   createdOn,
				UUID:        events.EventUUID(msgUUID),
				OrgID:       orgID,
				ContactUUID: core.ContactUUID(contactUUID),
				URNPath:     urnPath,
				Text:        text,
				InTicket:    ticketUUID != "",
			}

			doc, err := json.Marshal(msg)
			if err != nil {
				rows.Close()
				return fmt.Errorf("error marshalling message doc: %w", err)
			}

			rt.ES.Writer.Queue(&elastic.Document{
				Index:   msg.IndexName(rt.Config.ElasticMessagesIndex),
				ID:      string(msg.UUID),
				Routing: fmt.Sprintf("%d", msg.OrgID),
				Body:    doc,
			})

			batchCount++
			lastUUID = msgUUID
			lastCreatedOn = createdOn
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("error iterating message rows: %w", err)
		}
		rows.Close()

		if batchCount == 0 {
			break
		}

		numIndexed += batchCount
		startUUID = lastUUID

		fmt.Printf(" > Indexed %d messages (last uuid=%s, created_on=%s)\n", numIndexed, lastUUID, lastCreatedOn.Format(time.RFC3339))

		if batchCount < indexBatchSize {
			break
		}
	}

	fmt.Printf("Done. Indexed %d messages total.\n", numIndexed)
	return nil
}
