package search

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
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

// BuildElasticQuery turns the passed in contact ql query into an elastic query
func BuildElasticQuery(oa *models.OrgAssets, group *models.Group, status models.ContactStatus, excludeIDs []models.ContactID, query *contactql.ContactQuery) elastic.Query {
	return buildContactQuery(oa, group, status, excludeIDs, query, false)
}

// BuildContactQuery turns the passed in contact ql query into a query for the given backend
func BuildContactQuery(oa *models.OrgAssets, group *models.Group, status models.ContactStatus, excludeIDs []models.ContactID, query *contactql.ContactQuery, os bool) elastic.Query {
	return buildContactQuery(oa, group, status, excludeIDs, query, os)
}

func buildContactQuery(oa *models.OrgAssets, group *models.Group, status models.ContactStatus, excludeIDs []models.ContactID, query *contactql.ContactQuery, os bool) elastic.Query {
	// use filter context for all clauses since we sort by id, not relevance score, and filter clauses
	// are cacheable and skip scoring
	filter := []elastic.Query{
		elastic.Term("org_id", oa.OrgID()),
	}

	// Elastic has is_active field, OpenSearch only indexes active contacts
	if !os {
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
		not := []elastic.Query{}
		if os {
			// OpenSearch uses db_id for the database contact ID
			ids := make([]int64, len(excludeIDs))
			for i := range excludeIDs {
				ids[i] = int64(excludeIDs[i])
			}
			not = append(not, elastic.Query{"terms": map[string]any{"db_id": ids}})
		} else {
			// Elastic uses _id for the database contact ID
			ids := make([]string, len(excludeIDs))
			for i := range excludeIDs {
				ids[i] = fmt.Sprintf("%d", excludeIDs[i])
			}
			not = append(not, elastic.Ids(ids...))
		}
		bq["must_not"] = not
	}

	return elastic.Query{"bool": bq}
}

// GetContactTotal returns the total count of matching contacts for the given query
func GetContactTotal(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, query string, os bool) (*contactql.ContactQuery, int64, error) {
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

	eq := buildContactQuery(oa, group, status, nil, parsed, os)
	src := map[string]any{"query": eq}

	if os {
		resp, err := rt.OS.Client.Indices.Count(ctx, &opensearchapi.IndicesCountReq{
			Indices: []string{rt.Config.OSContactsIndex},
			Body:    bytes.NewReader(jsonx.MustMarshal(src)),
			Params:  opensearchapi.IndicesCountParams{Routing: []string{oa.OrgID().String()}},
		})
		if err != nil {
			return nil, 0, fmt.Errorf("error performing count: %w", err)
		}
		return parsed, int64(resp.Count), nil
	}

	count, err := rt.ES.Count().Index(rt.Config.ElasticContactsIndex).Routing(oa.OrgID().String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("error performing count: %w", err)
	}

	return parsed, count.Count, nil
}

// GetContactIDsForQueryPage returns a page of contact ids for the given query and sort
func GetContactIDsForQueryPage(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, excludeIDs []models.ContactID, query string, sort string, offset int, pageSize int, os bool) (*contactql.ContactQuery, []models.ContactID, int64, error) {
	env := oa.Env()
	start := time.Now()
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

	eq := buildContactQuery(oa, group, status, excludeIDs, parsed, os)

	fieldSort, err := es.ToElasticSort(sort, oa.SessionAssets())
	if err != nil {
		return nil, nil, 0, fmt.Errorf("error parsing sort: %w", err)
	}

	if os {
		fieldSort = adaptSortForOS(fieldSort)

		src := map[string]any{
			"_source":          []string{"db_id"},
			"query":            eq,
			"sort":             []any{fieldSort},
			"from":             offset,
			"size":             pageSize,
			"track_total_hits": true,
		}

		routing := oa.OrgID().String()
		resp, err := rt.OS.Client.Search(ctx, &opensearchapi.SearchReq{
			Indices: []string{rt.Config.OSContactsIndex},
			Body:    bytes.NewReader(jsonx.MustMarshal(src)),
			Params:  opensearchapi.SearchParams{Routing: []string{routing}},
		})
		if err != nil {
			return nil, nil, 0, fmt.Errorf("error performing query: %w", err)
		}

		ids := make([]models.ContactID, 0, pageSize)
		ids = appendIDsFromOSHits(ids, resp.Hits.Hits)

		slog.Debug("paged contact query complete", "org_id", oa.OrgID(), "query", query, "elapsed", time.Since(start), "page_count", len(ids), "total_count", resp.Hits.Total.Value)

		return parsed, ids, int64(resp.Hits.Total.Value), nil
	}

	index := rt.Config.ElasticContactsIndex
	src := map[string]any{
		"_source":          false,
		"query":            eq,
		"sort":             []any{fieldSort},
		"from":             offset,
		"size":             pageSize,
		"track_total_hits": true,
	}

	results, err := rt.ES.Search().Index(index).Routing(oa.OrgID().String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("error performing query: %w", err)
	}

	ids := make([]models.ContactID, 0, pageSize)
	ids = appendIDsFromESHits(ids, results.Hits.Hits)

	slog.Debug("paged contact query complete", "org_id", oa.OrgID(), "query", query, "elapsed", time.Since(start), "page_count", len(ids), "total_count", results.Hits.Total.Value)

	return parsed, ids, results.Hits.Total.Value, nil
}

// GetContactIDsForQuery returns up to limit the contact ids that match the given query, sorted by id. Limit of -1 means return all.
func GetContactIDsForQuery(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, group *models.Group, status models.ContactStatus, query string, limit int, os bool) ([]models.ContactID, error) {
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

	eq := buildContactQuery(oa, group, status, nil, parsed, os)

	if os {
		return getContactIDsForQueryOS(ctx, rt, oa, eq, limit)
	}
	return getContactIDsForQueryES(ctx, rt, oa, eq, limit)
}

func getContactIDsForQueryES(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, eq elastic.Query, limit int) ([]models.ContactID, error) {
	index := rt.Config.ElasticContactsIndex
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

		results, err := rt.ES.Search().Index(index).Routing(oa.OrgID().String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("error searching ES index: %w", err)
		}
		return appendIDsFromESHits(ids, results.Hits.Hits), nil
	}

	// for larger limits we need to take a point in time and iterate through multiple search requests using search_after
	pit, err := rt.ES.OpenPointInTime(index).Routing(oa.OrgID().String()).KeepAlive("1m").Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("error creating ES point-in-time: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := rt.ES.ClosePointInTime().Id(pit.Id).Do(cctx); err != nil {
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
		results, err := rt.ES.Search().Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
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

func getContactIDsForQueryOS(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, eq elastic.Query, limit int) ([]models.ContactID, error) {
	index := rt.Config.OSContactsIndex
	routing := oa.OrgID().String()
	sort := elastic.SortBy("db_id", true)
	ids := make([]models.ContactID, 0, 100)

	// if limit provided that can be done with single search, do that
	if limit >= 0 && limit <= 10_000 {
		src := map[string]any{
			"_source":          []string{"db_id"},
			"query":            eq,
			"sort":             []any{sort},
			"from":             0,
			"size":             limit,
			"track_total_hits": false,
		}

		resp, err := rt.OS.Client.Search(ctx, &opensearchapi.SearchReq{
			Indices: []string{index},
			Body:    bytes.NewReader(jsonx.MustMarshal(src)),
			Params:  opensearchapi.SearchParams{Routing: []string{routing}},
		})
		if err != nil {
			return nil, fmt.Errorf("error searching OS index: %w", err)
		}
		return appendIDsFromOSHits(ids, resp.Hits.Hits), nil
	}

	// for larger limits we need to take a point in time and iterate through multiple search requests using search_after
	pit, err := rt.OS.Client.PointInTime.Create(ctx, opensearchapi.PointInTimeCreateReq{
		Indices: []string{index},
		Params:  opensearchapi.PointInTimeCreateParams{KeepAlive: time.Minute, Routing: routing},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating OS point-in-time: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := rt.OS.Client.PointInTime.Delete(cctx, opensearchapi.PointInTimeDeleteReq{
			PitID: []string{pit.PitID},
		}); err != nil {
			slog.Error("error closing OS point-in-time", "error", err)
		}
	}()

	src := map[string]any{
		"_source":          []string{"db_id"},
		"query":            eq,
		"sort":             []any{sort},
		"pit":              map[string]any{"id": pit.PitID, "keep_alive": "1m"},
		"size":             10_000,
		"track_total_hits": false,
	}

	for {
		resp, err := rt.OS.Client.Search(ctx, &opensearchapi.SearchReq{
			Body: bytes.NewReader(jsonx.MustMarshal(src)),
		})
		if err != nil {
			return nil, fmt.Errorf("error searching OS index: %w", err)
		}

		if len(resp.Hits.Hits) == 0 {
			break
		}

		ids = appendIDsFromOSHits(ids, resp.Hits.Hits)

		if limit != -1 && len(ids) >= limit {
			ids = ids[:limit]
			break
		}
		if limit != -1 {
			if remaining := limit - len(ids); remaining < 10_000 {
				src["size"] = remaining
			}
		}

		lastHit := resp.Hits.Hits[len(resp.Hits.Hits)-1]
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

// appendIDsFromOSHits extracts contact IDs from OpenSearch hits using the db_id source field
func appendIDsFromOSHits(ids []models.ContactID, hits []opensearchapi.SearchHit) []models.ContactID {
	for _, hit := range hits {
		var doc struct {
			DBID models.ContactID `json:"db_id"`
		}
		if err := json.Unmarshal(hit.Source, &doc); err == nil && doc.DBID != models.NilContactID {
			ids = append(ids, doc.DBID)
		}
	}
	return ids
}

// adaptSortForOS replaces "id" with "db_id" in sort specs for OpenSearch
func adaptSortForOS(s elastic.Sort) elastic.Sort {
	adapted := make(elastic.Sort, len(s))
	for k, v := range s {
		if k == "id" {
			adapted["db_id"] = v
		} else {
			adapted[k] = v
		}
	}
	return adapted
}
