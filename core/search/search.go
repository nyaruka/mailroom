package search

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
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

func buildContactQuery(oa *models.OrgAssets, group *models.Group, status models.ContactStatus, excludeIDs []models.ContactID, query *contactql.ContactQuery, v2 bool) elastic.Query {
	// use filter context for all clauses since we never sort by relevance score, and filter clauses
	// are cacheable and skip scoring
	filter := []elastic.Query{
		elastic.Term("org_id", oa.OrgID()),
	}

	// rp-indexer index has is_active field, v2 index only indexes active contacts
	if !v2 {
		filter = append(filter, elastic.Term("is_active", true))
	}

	if group != nil {
		filter = append(filter, elastic.Term("group_ids", group.ID()))
	}

	if status != models.NilContactStatus {
		filter = append(filter, elastic.Term("status", status))
	}

	if query != nil {
		filter = append(filter, es.ToElasticQuery(oa.Env(), assetMapper, query))
	}

	bq := map[string]any{"filter": filter}

	if len(excludeIDs) > 0 {
		ids := make([]string, len(excludeIDs))
		for i := range excludeIDs {
			ids[i] = fmt.Sprintf("%d", excludeIDs[i])
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

	v1Count, err := getContactTotal(ctx, rt, oa, group, status, parsed, false)
	if err != nil {
		return nil, 0, err
	}

	if rt.Config.ElasticContactsV2Verify {
		v2Count, v2Err := getContactTotal(ctx, rt, oa, group, status, parsed, true)
		if v2Err != nil {
			slog.Warn("error counting v2 contacts index for comparison", "org_id", oa.OrgID(), "error", v2Err)
		} else if v1Count != v2Count {
			slog.Error("v1/v2 contacts index count mismatch", "org_id", oa.OrgID(), "query", query, "v1_total", v1Count, "v2_total", v2Count)
		}
	}

	return parsed, v1Count, nil
}

func getContactTotal(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, status models.ContactStatus, parsed *contactql.ContactQuery, v2 bool) (int64, error) {
	eq := buildContactQuery(oa, group, status, nil, parsed, v2)
	src := map[string]any{"query": eq}

	index := rt.Config.ElasticContactsIndex
	if v2 {
		index = rt.Config.ElasticContactsIndexV2
	}

	count, err := rt.ES.Client.Count().Index(index).Routing(oa.OrgID().String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("error performing count: %w", err)
	}

	return count.Count, nil
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

	fieldSort, err := es.ToElasticSort(sort, oa.SessionAssets())
	if err != nil {
		return nil, nil, 0, fmt.Errorf("error parsing sort: %w", err)
	}

	v1Start := time.Now()
	v1Hits, v1Total, err := getContactIDsForQueryPage(ctx, rt, oa, group, status, excludeIDs, parsed, fieldSort, offset, pageSize, false)
	if err != nil {
		return nil, nil, 0, err
	}
	rt.Stats.RecordContactSearch("v1", time.Since(v1Start))

	if rt.Config.ElasticContactsV2Verify {
		v2Start := time.Now()
		v2Hits, v2Total, v2Err := getContactIDsForQueryPage(ctx, rt, oa, group, status, excludeIDs, parsed, fieldSort, offset, pageSize, true)
		rt.Stats.RecordContactSearch("v2", time.Since(v2Start))

		if v2Err != nil {
			slog.Warn("error searching v2 contacts index for comparison", "org_id", oa.OrgID(), "error", v2Err)
		} else if v1Total != v2Total || !contactIDsEqual(v1Hits, v2Hits) {
			example := findMismatchExample(v1Hits, v2Hits)
			slog.Error("v1/v2 contacts index search mismatch",
				"org_id", oa.OrgID(),
				"query", query,
				"v1_total", v1Total,
				"v2_total", v2Total,
				"v1_page_count", len(v1Hits),
				"v2_page_count", len(v2Hits),
				"example_contact", example,
			)
		}
	}

	return parsed, v1Hits, v1Total, nil
}

func getContactIDsForQueryPage(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, status models.ContactStatus, excludeIDs []models.ContactID, parsed *contactql.ContactQuery, fieldSort map[string]any, offset int, pageSize int, v2 bool) ([]models.ContactID, int64, error) {
	start := time.Now()
	eq := buildContactQuery(oa, group, status, excludeIDs, parsed, v2)

	index := rt.Config.ElasticContactsIndex
	if v2 {
		index = rt.Config.ElasticContactsIndexV2
	}

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

	v1Eq := buildContactQuery(oa, group, status, nil, parsed, false)
	v1IDs, err := getContactIDsForQuery(ctx, rt, oa, rt.Config.ElasticContactsIndex, v1Eq, limit)
	if err != nil {
		return nil, err
	}

	if rt.Config.ElasticContactsV2Verify {
		v2Eq := buildContactQuery(oa, group, status, nil, parsed, true)
		v2IDs, v2Err := getContactIDsForQuery(ctx, rt, oa, rt.Config.ElasticContactsIndexV2, v2Eq, limit)
		if v2Err != nil {
			slog.Warn("error searching v2 contacts index for comparison", "org_id", oa.OrgID(), "error", v2Err)
		} else if !contactIDsEqual(v1IDs, v2IDs) {
			example := findMismatchExample(v1IDs, v2IDs)
			slog.Error("v1/v2 contacts index search mismatch",
				"org_id", oa.OrgID(),
				"query", query,
				"v1_count", len(v1IDs),
				"v2_count", len(v2IDs),
				"example_contact", example,
			)
		}
	}

	return v1IDs, nil
}

// GetContactIDsForQueryV2 searches only the v2 contacts index. This is intended for tests that verify v2 indexing.
func GetContactIDsForQueryV2(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, status models.ContactStatus, query string, limit int) ([]models.ContactID, error) {
	env := oa.Env()
	var parsed *contactql.ContactQuery
	var err error

	if query != "" {
		parsed, err = contactql.ParseQuery(env, query, oa.SessionAssets())
		if err != nil {
			return nil, fmt.Errorf("error parsing query: %s: %w", query, err)
		}
	}

	if group != nil && !group.Visible() {
		status = models.ContactStatus(group.Type())
		group = nil
	}

	eq := buildContactQuery(oa, group, status, nil, parsed, true)
	return getContactIDsForQuery(ctx, rt, oa, rt.Config.ElasticContactsIndexV2, eq, limit)
}

func getContactIDsForQuery(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, index string, eq elastic.Query, limit int) ([]models.ContactID, error) {
	sort := elastic.SortBy("id", true)
	ids := make([]models.ContactID, 0, 100)

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

// appendIDsFromESHits extracts contact IDs from Elasticsearch hits where _id is the database contact ID
func appendIDsFromESHits(ids []models.ContactID, hits []types.Hit) []models.ContactID {
	for _, hit := range hits {
		id, err := strconv.Atoi(*hit.Id_)
		if err == nil {
			ids = append(ids, models.ContactID(id))
		}
	}
	return ids
}

// contactIDsEqual returns true if two slices contain the same contact IDs in the same order
func contactIDsEqual(a, b []models.ContactID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// findMismatchExample returns a description of the first contact ID that differs between v1 and v2 results
func findMismatchExample(v1IDs, v2IDs []models.ContactID) string {
	v2Set := make(map[models.ContactID]bool, len(v2IDs))
	for _, id := range v2IDs {
		v2Set[id] = true
	}
	for _, id := range v1IDs {
		if !v2Set[id] {
			return fmt.Sprintf("contact %d in v1 but not v2", id)
		}
	}

	v1Set := make(map[models.ContactID]bool, len(v1IDs))
	for _, id := range v1IDs {
		v1Set[id] = true
	}
	for _, id := range v2IDs {
		if !v1Set[id] {
			return fmt.Sprintf("contact %d in v2 but not v1", id)
		}
	}

	return "same IDs but different order"
}
