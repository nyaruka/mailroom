package search

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/elastic/go-elasticsearch/v8/typedapi/types"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/goflow/contactql/es"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// AssetMapper maps resolved assets in queries to how we identify them in ES which in the case
// of flows and groups is their ids. We can do this by just type cracking them to their models.
type AssetMapper struct{}

func (m *AssetMapper) Flow(f assets.Flow) int64 {
	return int64(f.(*models.Flow).ID())
}

func (m *AssetMapper) Group(g assets.Group) int64 {
	return int64(g.(*models.Group).ID())
}

var assetMapper = &AssetMapper{}

func newConverter(oa *models.OrgAssets, uuidAsDocID bool) *es.Converter {
	return es.NewConverter(oa.Env(), assetMapper, uuidAsDocID)
}

func buildContactQuery(oa *models.OrgAssets, group *models.Group, status models.ContactStatus, excludeUUIDs []flows.ContactUUID, query *contactql.ContactQuery) elastic.Query {
	// use filter context for all clauses since we never sort by relevance score, and filter clauses
	// are cacheable and skip scoring
	filter := []elastic.Query{
		elastic.Term("org_id", oa.OrgID()),
	}

	if group != nil {
		filter = append(filter, elastic.Term("group_ids", group.ID()))
	}

	if status != models.NilContactStatus {
		filter = append(filter, elastic.Term("status", status))
	}

	if query != nil {
		conv := newConverter(oa, true)
		filter = append(filter, conv.Query(query))
	}

	bq := map[string]any{"filter": filter}

	if len(excludeUUIDs) > 0 {
		ids := make([]string, len(excludeUUIDs))
		for i := range excludeUUIDs {
			ids[i] = string(excludeUUIDs[i])
		}
		bq["must_not"] = []elastic.Query{elastic.Ids(ids...)}
	}

	return elastic.Query{"bool": bq}
}

// GetContactTotal returns the total count of matching contacts for the given query
func GetContactTotal(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, query string) (*contactql.ContactQuery, int64, error) {
	env := oa.Env()
	var parsed *contactql.ContactQuery
	var err error

	if query != "" {
		parsed, err = contactql.ParseQuery(env, query, oa.SessionAssets())
		if err != nil {
			return nil, 0, fmt.Errorf("error parsing query: %s: %w", query, err)
		}
	}

	// if group is a status group, the index won't know about it so search by status instead
	status := models.NilContactStatus
	if group != nil && !group.Visible() {
		status = models.ContactStatus(group.Type())
		group = nil
	}

	index := rt.Config.ElasticContactsIndex
	eq := buildContactQuery(oa, group, status, nil, parsed)
	src := map[string]any{"query": eq}

	count, err := rt.ES.Client.Count().Index(index).Routing(oa.OrgID().String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("error performing count: %w", err)
	}

	return parsed, count.Count, nil
}

// GetContactUUIDsForQueryPage returns a page of contact UUIDs for the given query and sort
func GetContactUUIDsForQueryPage(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, excludeUUIDs []flows.ContactUUID, query string, sort string, offset int, pageSize int) (*contactql.ContactQuery, []flows.ContactUUID, int64, error) {
	env := oa.Env()
	var parsed *contactql.ContactQuery
	var err error

	if query != "" {
		parsed, err = contactql.ParseQuery(env, query, oa.SessionAssets())
		if err != nil {
			return nil, nil, 0, fmt.Errorf("error parsing query: %s: %w", query, err)
		}
	}

	// if group is a status group, the index won't know about it so search by status instead
	status := models.NilContactStatus
	if group != nil && !group.Visible() {
		status = models.ContactStatus(group.Type())
		group = nil
	}

	conv := newConverter(oa, true)
	fieldSort, err := conv.Sort(sort, oa.SessionAssets())
	if err != nil {
		return nil, nil, 0, fmt.Errorf("error parsing sort: %w", err)
	}

	start := time.Now()
	hits, total, err := getContactUUIDsForQueryPage(ctx, rt, oa, group, status, excludeUUIDs, parsed, fieldSort, offset, pageSize, rt.Config.ElasticContactsIndex)
	if err != nil {
		return nil, nil, 0, err
	}
	rt.Stats.RecordSearch("contacts", time.Since(start))

	return parsed, hits, total, nil
}

func getContactUUIDsForQueryPage(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, status models.ContactStatus, excludeUUIDs []flows.ContactUUID, parsed *contactql.ContactQuery, fieldSort map[string]any, offset int, pageSize int, index string) ([]flows.ContactUUID, int64, error) {
	start := time.Now()
	eq := buildContactQuery(oa, group, status, excludeUUIDs, parsed)

	src := map[string]any{
		"_source":          false,
		"query":            eq,
		"sort":             []any{fieldSort},
		"from":             offset,
		"size":             pageSize,
		"track_total_hits": true,
	}

	results, err := rt.ES.Client.Search().Index(index).Routing(oa.OrgID().String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("error performing query: %w", err)
	}

	uuids := make([]flows.ContactUUID, 0, pageSize)
	for _, hit := range results.Hits.Hits {
		uuids = append(uuids, flows.ContactUUID(*hit.Id_))
	}

	slog.Debug("paged contact query complete", "org_id", oa.OrgID(), "index", index, "elapsed", time.Since(start), "page_count", len(uuids), "total_count", results.Hits.Total.Value)

	return uuids, results.Hits.Total.Value, nil
}

// GetContactUUIDsForQuery returns up to limit the contact UUIDs that match the given query, sorted by contact ID. Limit of -1 means return all.
func GetContactUUIDsForQuery(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, status models.ContactStatus, query string, limit int) ([]flows.ContactUUID, error) {
	env := oa.Env()
	var parsed *contactql.ContactQuery
	var err error

	if query != "" {
		parsed, err = contactql.ParseQuery(env, query, oa.SessionAssets())
		if err != nil {
			return nil, fmt.Errorf("error parsing query: %s: %w", query, err)
		}
	}

	// if group is a status group, the index won't know about it so search by status instead
	if group != nil && !group.Visible() {
		status = models.ContactStatus(group.Type())
		group = nil
	}

	eq := buildContactQuery(oa, group, status, nil, parsed)

	return getContactUUIDsForQuery(ctx, rt, oa, rt.Config.ElasticContactsIndex, eq, limit)
}

func getContactUUIDsForQuery(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, index string, eq elastic.Query, limit int) ([]flows.ContactUUID, error) {
	sort := elastic.SortBy("id", true)
	uuids := make([]flows.ContactUUID, 0, 100)

	// if limit provided that can be done with single search, do that
	if limit >= 0 && limit <= 10_000 {
		src := map[string]any{
			"_source":          false,
			"query":            eq,
			"sort":             []any{sort},
			"from":             0,
			"size":             limit,
			"track_total_hits": false,
		}

		results, err := rt.ES.Client.Search().Index(index).Routing(oa.OrgID().String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("error searching ES index: %w", err)
		}
		return appendUUIDsFromESHits(uuids, results.Hits.Hits), nil
	}

	// for larger limits we need to take a point in time and iterate through multiple search requests using search_after
	pit, err := rt.ES.Client.OpenPointInTime(index).Routing(oa.OrgID().String()).KeepAlive("1m").Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("error creating ES point-in-time: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := rt.ES.Client.ClosePointInTime().Id(pit.Id).Do(cctx); err != nil {
			slog.Error("error closing ES point-in-time", "error", err)
		}
	}()

	src := map[string]any{
		"_source":          false,
		"query":            eq,
		"sort":             []any{sort},
		"pit":              map[string]any{"id": pit.Id, "keep_alive": "1m"},
		"size":             10_000,
		"track_total_hits": false,
	}

	for {
		results, err := rt.ES.Client.Search().Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("error searching ES index: %w", err)
		}

		if len(results.Hits.Hits) == 0 {
			break
		}

		uuids = appendUUIDsFromESHits(uuids, results.Hits.Hits)

		if limit != -1 && len(uuids) >= limit {
			uuids = uuids[:limit]
			break
		}
		if limit != -1 {
			if remaining := limit - len(uuids); remaining < 10_000 {
				src["size"] = remaining
			}
		}

		lastHit := results.Hits.Hits[len(results.Hits.Hits)-1]
		src["search_after"] = lastHit.Sort
	}

	return uuids, nil
}

// appendUUIDsFromESHits extracts contact UUIDs from Elasticsearch hits using the document _id
func appendUUIDsFromESHits(uuids []flows.ContactUUID, hits []types.Hit) []flows.ContactUUID {
	for _, hit := range hits {
		uuids = append(uuids, flows.ContactUUID(*hit.Id_))
	}
	return uuids
}

// GetContactIDsForQuery is a temporary wrapper around GetContactUUIDsForQuery that converts the results back to
// contact IDs. Used by call sites that still need IDs; these should be updated to work with UUIDs directly.
func GetContactIDsForQuery(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, status models.ContactStatus, query string, limit int) ([]models.ContactID, error) {
	uuids, err := GetContactUUIDsForQuery(ctx, rt, oa, group, status, query, limit)
	if err != nil {
		return nil, err
	}

	ids, err := models.GetContactIDsFromUUIDs(ctx, rt.DB, oa.OrgID(), uuids)
	if err != nil {
		return nil, err
	}

	slices.Sort(ids)

	return ids, nil
}
