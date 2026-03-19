package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

const batchSize = 500

// command line tool to re-index all contacts or messages.
//
// go install github.com/nyaruka/mailroom/cmd/mrindex; mrindex <contacts|messages>
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

	// parse mode from args
	flags := flag.NewFlagSet("mrindex", flag.ExitOnError)
	startUUID := flags.String("start-uuid", "", "UUID to start from (messages mode only, works backwards from here)")
	flags.Parse(os.Args[1:])

	mode := flags.Arg(0)
	if mode != "contacts" && mode != "messages" {
		fmt.Println("usage: mrindex [--start-uuid UUID] <contacts|messages>")
		os.Exit(1)
	}

	ctx := context.TODO()

	switch mode {
	case "contacts":
		if err := indexAllContacts(ctx, rt); err != nil {
			rt.Stop()
			fmt.Printf("error re-indexing contacts: %s\n", err)
			os.Exit(1)
		}
	case "messages":
		if err := indexAllMessages(ctx, rt, *startUUID); err != nil {
			rt.Stop()
			fmt.Printf("error re-indexing messages: %s\n", err)
			os.Exit(1)
		}
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
			contactIDs, err := models.GetContactIDsPage(ctx, rt.DB, orgID, afterID, batchSize)
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

			if len(contactIDs) < batchSize {
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
SELECT m.uuid, m.org_id, m.text, m.created_on, m.ticket_uuid, c.uuid AS contact_uuid
  FROM msgs_msg m
  JOIN contacts_contact c ON c.id = m.contact_id
 WHERE (m.direction = 'I' OR (m.broadcast_id IS NULL AND m.created_by_id IS NOT NULL))
   AND LENGTH(m.text) >= $3
   AND m.visibility IN ('V', 'A')
   AND m.msg_type != 'V'
   AND m.uuid < $1
 ORDER BY m.uuid DESC
 LIMIT $2`

func indexAllMessages(ctx context.Context, rt *runtime.Runtime, startUUID string) error {
	if startUUID == "" {
		startUUID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	}

	numIndexed := 0
	lastUUID := ""

	for {
		rows, err := rt.DB.QueryContext(ctx, sqlSelectMessagesForSearch, startUUID, batchSize, search.MessageTextMinLength)
		if err != nil {
			return fmt.Errorf("error querying messages: %w", err)
		}

		batchCount := 0
		var lastCreatedOn time.Time

		for rows.Next() {
			var msgUUID, contactUUID string
			var orgID models.OrgID
			var text string
			var createdOn time.Time
			var ticketUUID null.String

			if err := rows.Scan(&msgUUID, &orgID, &text, &createdOn, &ticketUUID, &contactUUID); err != nil {
				rows.Close()
				return fmt.Errorf("error scanning message row: %w", err)
			}

			msg := &search.MessageDoc{
				CreatedOn:   createdOn,
				UUID:        flows.EventUUID(msgUUID),
				OrgID:       orgID,
				ContactUUID: flows.ContactUUID(contactUUID),
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

		if batchCount < batchSize {
			break
		}
	}

	fmt.Printf("Done. Indexed %d messages total.\n", numIndexed)
	return nil
}
