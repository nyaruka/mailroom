package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/envs"

	"github.com/lib/pq"
)

// Location is our mailroom type for administrative locations
// TODO: convert to something less jank when we have real location constructors
type Location struct {
	id        int
	level     int
	parentID  *int
	osmID     string
	Name_     string      `json:"name"`
	Aliases_  []string    `json:"aliases"`
	Children_ []*Location `json:"children"`
}

// ID returns the database id for this location
func (l *Location) ID() int { return l.id }

// Level returns the level for this location
func (l *Location) Level() int { return l.level }

// OSMID returns the OSM ID for this location
func (l *Location) OSMID() string { return l.osmID }

// Name returns the name for this location
func (l *Location) Name() string { return l.Name_ }

// Aliases returns the list of aliases for this location
func (l *Location) Aliases() []string { return l.Aliases_ }

// Children returns the list of children for this location
func (l *Location) Children() []*Location { return l.Children_ }

// loadLocations loads all the locations for this org returning the root node
func loadLocations(ctx context.Context, db *sql.DB, oa *OrgAssets) ([]assets.LocationHierarchy, error) {
	start := time.Now()

	rows, err := db.QueryContext(ctx, loadLocationsSQL, oa.orgID)
	if err != nil {
		return nil, fmt.Errorf("error querying locations for org: %d: %w", oa.orgID, err)
	}
	defer rows.Close()

	// we first read in all our locations into a map by id
	locationMap := make(map[int]*Location)
	locations := make([]*Location, 0, 10)
	var root *Location
	maxLevel := 0

	for rows.Next() {
		location := &Location{}

		err := rows.Scan(&location.id, &location.level, &location.osmID, &location.parentID, &location.Name_, pq.Array(&location.Aliases_))
		if err != nil {
			return nil, fmt.Errorf("error scanning location row: %w", err)
		}

		if location.level > maxLevel {
			maxLevel = location.level
		}

		if location.parentID == nil {
			root = location
		}

		locationMap[location.id] = location
		locations = append(locations, location)
	}

	// no locations? no hierarchy
	if len(locations) == 0 {
		return []assets.LocationHierarchy{}, nil
	}

	// now we make another pass and associate all children
	for _, l := range locations {
		if l.parentID != nil {
			parent, found := locationMap[*l.parentID]
			if !found {
				return nil, fmt.Errorf("unable to find parent: %d for location: %d", *l.parentID, l.id)
			}
			parent.Children_ = append(parent.Children_, l)
		}
	}

	// ok, encode to json
	locationJSON, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("error marshalling json hierarchy: %w", err)
	}

	// then read it in
	hierarchy, err := envs.ReadLocationHierarchy(oa.Env(), locationJSON)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling hierarchy: %s: %w", string(locationJSON), err)
	}

	slog.Debug("loaded locations", "elapsed", time.Since(start), "org_id", oa.orgID)

	return []assets.LocationHierarchy{hierarchy}, nil
}

// TODO: this is a bit bananas
const loadLocationsSQL = `
SELECT
	l.id, 
	l.level,	
	l.osm_id, 
	l.parent_id, 
	l.name,
	(SELECT ARRAY_AGG(a.name) FROM (
		SELECT 
			DISTINCT(a.name)
		FROM 
			locations_locationalias a
		WHERE 
			a.location_id = l.id AND
			a.is_active = TRUE AND
			a.org_id = $1
		ORDER BY 
			a.name
	)a ) aliases
FROM
	locations_location l
WHERE
	l.lft >= (select lft from locations_location la, orgs_org o where la.id = o.location_id and o.id = $1) and 
	l.rght <= (select rght from locations_location la, orgs_org o where la.id = o.location_id and o.id = $1) and
	l.tree_id = (select tree_id from locations_location la, orgs_org o where la.id = o.location_id and o.id = $1)
ORDER BY
	l.level, l.id;
`
