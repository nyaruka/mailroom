package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/operationtype"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/shopspring/decimal"
)

// ContactDocField represents a single field value in a contact document.
type ContactDocField struct {
	Field           assets.FieldUUID `json:"field"`
	Text            string           `json:"text,omitempty"`
	Number          *decimal.Decimal `json:"number,omitempty"`
	Datetime        *time.Time       `json:"datetime,omitempty"`
	State           string           `json:"state,omitempty"`
	StateKeyword    string           `json:"state_keyword,omitempty"`
	District        string           `json:"district,omitempty"`
	DistrictKeyword string           `json:"district_keyword,omitempty"`
	Ward            string           `json:"ward,omitempty"`
	WardKeyword     string           `json:"ward_keyword,omitempty"`
}

// ContactDocURN represents a single URN in a contact document.
type ContactDocURN struct {
	Scheme string `json:"scheme"`
	Path   string `json:"path"`
}

// ContactDoc represents a contact document in the contacts index. UUID is used as the document _id.
type ContactDoc struct {
	DBID           models.ContactID     `json:"id"`
	UUID           core.ContactUUID     `json:"-"` // used as _id, not in body
	OrgID          models.OrgID         `json:"org_id"`
	Name           string               `json:"name,omitempty"`
	Status         models.ContactStatus `json:"status"`
	Language       i18n.Language        `json:"language,omitempty"`
	Fields         []*ContactDocField   `json:"fields,omitempty"`
	URNs           []*ContactDocURN     `json:"urns,omitempty"`
	GroupIDs       []models.GroupID     `json:"group_ids,omitempty"`
	FlowID         models.FlowID        `json:"flow_id,omitempty"`
	FlowHistoryIDs []models.FlowID      `json:"flow_history_ids,omitempty"`
	Tickets        int                  `json:"tickets"`
	CreatedOn      time.Time            `json:"created_on"`
	LastSeenOn     *time.Time           `json:"last_seen_on,omitempty"`
}

// NewContactDoc builds a ContactDoc from an engine contact and its org assets. We use the engine contact
// rather than the DB contact because it is kept up-to-date in memory as events are applied.
func NewContactDoc(oa *models.OrgAssets, c *core.Contact, currentFlowID models.FlowID, flowHistoryIDs []models.FlowID) *ContactDoc {
	doc := &ContactDoc{
		UUID:           c.UUID(),
		DBID:           models.ContactID(c.ID()),
		OrgID:          oa.OrgID(),
		Name:           c.Name(),
		Status:         models.ContactToModelStatus[c.Status()],
		Language:       c.Language(),
		CreatedOn:      c.CreatedOn(),
		LastSeenOn:     c.LastSeenOn(),
		Tickets:        c.Tickets().Open().Count(),
		FlowID:         currentFlowID,
		FlowHistoryIDs: flowHistoryIDs,
	}

	// build field docs from the engine contact's field values
	for key, fv := range c.Fields() {
		if fv == nil {
			continue
		}

		value := fv.Value
		if value == nil {
			continue
		}

		field := oa.FieldByKey(key)
		if field == nil {
			continue
		}

		fd := &ContactDocField{Field: field.UUID()}

		if value.Text != nil && !value.Text.Empty() {
			fd.Text = value.Text.Native()
		}
		if value.Number != nil {
			n := value.Number.Native()
			fd.Number = &n
		}
		if value.Datetime != nil {
			t := value.Datetime.Native()
			fd.Datetime = &t
		}
		if value.State != "" {
			fd.State = string(value.State)
			fd.StateKeyword = value.State.Name()
		}
		if value.District != "" {
			fd.District = string(value.District)
			fd.DistrictKeyword = value.District.Name()
		}
		if value.Ward != "" {
			fd.Ward = string(value.Ward)
			fd.WardKeyword = value.Ward.Name()
		}

		doc.Fields = append(doc.Fields, fd)
	}

	// build URN docs
	for _, urn := range c.URNs() {
		doc.URNs = append(doc.URNs, &ContactDocURN{Scheme: urn.Scheme, Path: urn.Path})
	}

	// build group IDs by looking up the flow group UUIDs in the org assets
	for _, group := range c.Groups().All() {
		dbGroup := oa.GroupByUUID(group.UUID())
		if dbGroup != nil {
			doc.GroupIDs = append(doc.GroupIDs, dbGroup.ID())
		}
	}

	return doc
}

// IndexContacts builds contact documents and queues them for indexing in Elastic.
func IndexContacts(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contacts []*core.Contact, currentFlows map[models.ContactID]models.FlowID) error {
	if len(contacts) == 0 {
		return nil
	}

	contactIDs := make([]models.ContactID, len(contacts))
	for i, c := range contacts {
		contactIDs[i] = models.ContactID(c.ID())
	}

	flowHistoryByContact, err := models.GetContactFlowHistory(ctx, rt.DB, contactIDs)
	if err != nil {
		return fmt.Errorf("error loading flow history IDs: %w", err)
	}

	for _, c := range contacts {
		contactID := models.ContactID(c.ID())
		doc := NewContactDoc(oa, c, currentFlows[contactID], flowHistoryByContact[contactID])

		body, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("error marshalling contact doc: %w", err)
		}

		rt.ES.Writer.Queue(&elastic.Document{
			Index:   rt.Config.ElasticContactsIndex,
			ID:      string(doc.UUID),
			Routing: doc.OrgID.String(),
			Version: time.Now().UnixNano(),
			Body:    body,
		})
	}

	return nil
}

// DeindexContactsByUUID de-indexes the contacts with the given UUIDs from Elastic
func DeindexContactsByUUID(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, contactUUIDs []core.ContactUUID) (int, error) {
	routing := orgID.String()

	return deindexContactDocs(ctx, rt, contactUUIDs, func(core.ContactUUID) string { return routing })
}

// deindexContactDocs bulk-deletes the given contact docs from Elastic. The index uses custom routing so each delete
// must be routed to the org of the doc it's deleting.
func deindexContactDocs(ctx context.Context, rt *runtime.Runtime, contactUUIDs []core.ContactUUID, routing func(core.ContactUUID) string) (int, error) {
	if len(contactUUIDs) == 0 {
		return 0, nil // ES rejects an empty bulk request
	}

	cmds := &bytes.Buffer{}
	for _, uuid := range contactUUIDs {
		cmds.Write(jsonx.MustMarshal(map[string]any{"delete": map[string]any{"_id": string(uuid), "routing": routing(uuid)}}))
		cmds.WriteString("\n")
	}

	resp, err := rt.ES.Client.Bulk().Index(rt.Config.ElasticContactsIndex).Raw(bytes.NewReader(cmds.Bytes())).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("error deindexing deleted contacts from elastic: %w", err)
	}

	deleted := 0
	for _, r := range resp.Items {
		if r[operationtype.Delete].Status == 200 {
			deleted++
		}
	}

	return deleted, nil
}

const pruneBatchSize = 10_000

// PruneCounts are the running totals reported by PruneContacts.
type PruneCounts struct {
	Scanned  int
	Orphaned int
	Deleted  int
}

// PruneContacts scans the entire contacts index for orphaned documents - docs whose contact no longer exists in the
// database or has been released - and optionally deletes them. If progress is non-nil it is called with the running
// totals after each scanned batch.
func PruneContacts(ctx context.Context, rt *runtime.Runtime, del bool, progress func(PruneCounts)) (PruneCounts, error) {
	counts := PruneCounts{}

	pit, err := rt.ES.Client.OpenPointInTime(rt.Config.ElasticContactsIndex).KeepAlive("1m").Do(ctx)
	if err != nil {
		return counts, fmt.Errorf("error creating ES point-in-time: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := rt.ES.Client.ClosePointInTime().Id(pit.Id).Do(cctx); err != nil {
			slog.Error("error closing ES point-in-time", "error", err)
		}
	}()

	src := map[string]any{
		"_source":          []string{"org_id"},
		"sort":             []any{map[string]any{"_shard_doc": "asc"}},
		"pit":              map[string]any{"id": pit.Id, "keep_alive": "1m"},
		"size":             pruneBatchSize,
		"track_total_hits": false,
	}

	for {
		results, err := rt.ES.Client.Search().Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
		if err != nil {
			return counts, fmt.Errorf("error searching ES index: %w", err)
		}

		if len(results.Hits.Hits) == 0 {
			break
		}

		uuids := make([]core.ContactUUID, len(results.Hits.Hits))
		routings := make(map[core.ContactUUID]string, len(results.Hits.Hits))
		for i, hit := range results.Hits.Hits {
			var doc struct {
				OrgID models.OrgID `json:"org_id"`
			}
			if err := json.Unmarshal(hit.Source_, &doc); err != nil {
				return counts, fmt.Errorf("error unmarshalling contact doc %s: %w", *hit.Id_, err)
			}
			uuids[i] = core.ContactUUID(*hit.Id_)
			routings[uuids[i]] = doc.OrgID.String()
		}

		counts.Scanned += len(uuids)

		orphans, err := getOrphanedUUIDs(ctx, rt, uuids)
		if err != nil {
			return counts, err
		}

		counts.Orphaned += len(orphans)

		if del && len(orphans) > 0 {
			// re-check against the database immediately before deleting
			orphans, err = getOrphanedUUIDs(ctx, rt, orphans)
			if err != nil {
				return counts, err
			}

			deleted, err := deindexContactDocs(ctx, rt, orphans, func(uuid core.ContactUUID) string { return routings[uuid] })
			if err != nil {
				return counts, err
			}

			counts.Deleted += deleted
		}

		if progress != nil {
			progress(counts)
		}

		lastHit := results.Hits.Hits[len(results.Hits.Hits)-1]
		src["search_after"] = lastHit.Sort
	}

	return counts, nil
}

// getOrphanedUUIDs returns which of the given contact UUIDs don't belong to an active contact in the database
func getOrphanedUUIDs(ctx context.Context, rt *runtime.Runtime, uuids []core.ContactUUID) ([]core.ContactUUID, error) {
	existing, err := models.GetActiveContactUUIDs(ctx, rt.DB, uuids)
	if err != nil {
		return nil, fmt.Errorf("error checking contact UUIDs against database: %w", err)
	}

	existingSet := make(map[core.ContactUUID]bool, len(existing))
	for _, uuid := range existing {
		existingSet[uuid] = true
	}

	orphans := make([]core.ContactUUID, 0, len(uuids)-len(existing))
	for _, uuid := range uuids {
		if !existingSet[uuid] {
			orphans = append(orphans, uuid)
		}
	}
	return orphans, nil
}

// DeindexContactsByOrg de-indexes all contacts in the given org from Elastic
func DeindexContactsByOrg(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, limit int) (int, error) {
	src := map[string]any{
		"query":    elastic.Term("org_id", orgID),
		"max_docs": limit,
	}

	resp, err := rt.ES.Client.DeleteByQuery(rt.Config.ElasticContactsIndex).Routing(orgID.String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("error deindexing contacts in org #%d from elastic: %w", orgID, err)
	}

	return int(*resp.Deleted), nil
}
