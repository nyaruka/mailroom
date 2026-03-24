package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/elastic/go-elasticsearch/v8/typedapi/types"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/goflow/contactql/es"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

func contactsIndex(rt *runtime.Runtime) (string, bool) {
	if rt.Config.ElasticContactsUseOwn {
		return rt.Config.ElasticContactsIndex, true
	}
	return rt.Config.ElasticContactsLegacyIndex, false
}

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

func buildContactQuery(oa *models.OrgAssets, group *models.Group, status models.ContactStatus, excludeIDs []models.ContactID, query *contactql.ContactQuery, own bool) elastic.Query {
	// use filter context for all clauses since we never sort by relevance score, and filter clauses
	// are cacheable and skip scoring
	filter := []elastic.Query{
		elastic.Term("org_id", oa.OrgID()),
	}

	// rp-indexer index has is_active field, own index only indexes active contacts
	if !own {
		filter = append(filter, elastic.Term("is_active", true))
	}

	if group != nil {
		filter = append(filter, elastic.Term("group_ids", group.ID()))
	}

	if status != models.NilContactStatus {
		filter = append(filter, elastic.Term("status", status))
	}

	if query != nil {
		conv := newConverter(oa, own)
		filter = append(filter, conv.Query(query))
	}

	bq := map[string]any{"filter": filter}

	if len(excludeIDs) > 0 {
		ids := make([]any, len(excludeIDs))
		for i := range excludeIDs {
			ids[i] = excludeIDs[i]
		}
		bq["must_not"] = []elastic.Query{{"terms": map[string]any{"id": ids}}}
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

	index, own := contactsIndex(rt)
	eq := buildContactQuery(oa, group, status, nil, parsed, own)
	src := map[string]any{"query": eq}

	count, err := rt.ES.Client.Count().Index(index).Routing(oa.OrgID().String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("error performing count: %w", err)
	}

	return parsed, count.Count, nil
}

// GetContactIDsForQueryPage returns a page of contact ids for the given query and sort
func GetContactIDsForQueryPage(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, excludeIDs []models.ContactID, query string, sort string, offset int, pageSize int) (*contactql.ContactQuery, []models.ContactID, int64, error) {
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

	index, own := contactsIndex(rt)

	conv := newConverter(oa, own)
	fieldSort, err := conv.Sort(sort, oa.SessionAssets())
	if err != nil {
		return nil, nil, 0, fmt.Errorf("error parsing sort: %w", err)
	}

	start := time.Now()
	hits, total, err := getContactIDsForQueryPage(ctx, rt, oa, group, status, excludeIDs, parsed, fieldSort, offset, pageSize, index, own)
	if err != nil {
		return nil, nil, 0, err
	}
	rt.Stats.RecordSearch("contacts", time.Since(start))

	return parsed, hits, total, nil
}

func getContactIDsForQueryPage(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, status models.ContactStatus, excludeIDs []models.ContactID, parsed *contactql.ContactQuery, fieldSort map[string]any, offset int, pageSize int, index string, own bool) ([]models.ContactID, int64, error) {
	start := time.Now()
	eq := buildContactQuery(oa, group, status, excludeIDs, parsed, own)

	src := map[string]any{
		"_source":          false,
		"docvalue_fields":  []string{"id"},
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

	ids := make([]models.ContactID, 0, pageSize)
	ids = appendIDsFromESHits(ids, results.Hits.Hits)

	slog.Debug("paged contact query complete", "org_id", oa.OrgID(), "index", index, "elapsed", time.Since(start), "page_count", len(ids), "total_count", results.Hits.Total.Value)

	return ids, results.Hits.Total.Value, nil
}

// GetContactIDsForQuery returns up to limit the contact ids that match the given query, sorted by id. Limit of -1 means return all.
func GetContactIDsForQuery(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, status models.ContactStatus, query string, limit int) ([]models.ContactID, error) {
	env := oa.Env()
	var parsed *contactql.ContactQuery
	var err error

	// turn into elastic query
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

	index, own := contactsIndex(rt)
	eq := buildContactQuery(oa, group, status, nil, parsed, own)
	return getContactIDsForQuery(ctx, rt, oa, index, eq, limit)
}

func getContactIDsForQuery(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, index string, eq elastic.Query, limit int) ([]models.ContactID, error) {
	sort := elastic.SortBy("id", true)
	ids := make([]models.ContactID, 0, 100)

	// if limit provided that can be done with single search, do that
	if limit >= 0 && limit <= 10_000 {
		src := map[string]any{
			"_source":          false,
		"docvalue_fields":  []string{"id"},
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
		return appendIDsFromESHits(ids, results.Hits.Hits), nil
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
		"docvalue_fields":  []string{"id"},
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

		ids = appendIDsFromESHits(ids, results.Hits.Hits)

		if limit != -1 && len(ids) >= limit {
			ids = ids[:limit]
			break
		}
		if limit != -1 {
			if remaining := limit - len(ids); remaining < 10_000 {
				src["size"] = remaining
			}
		}

		lastHit := results.Hits.Hits[len(results.Hits.Hits)-1]
		src["search_after"] = lastHit.Sort
	}

	return ids, nil
}

// appendIDsFromESHits extracts contact IDs from Elasticsearch hits using the id docvalue field
func appendIDsFromESHits(ids []models.ContactID, hits []types.Hit) []models.ContactID {
	for _, hit := range hits {
		raw, ok := hit.Fields["id"]
		if !ok {
			continue
		}
		var vals []models.ContactID
		if err := json.Unmarshal(raw, &vals); err == nil && len(vals) > 0 {
			ids = append(ids, vals[0])
		}
	}
	return ids
}
