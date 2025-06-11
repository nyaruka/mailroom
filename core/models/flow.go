package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/null/v3"

	"github.com/jmoiron/sqlx"
)

// FlowID is the type for flow IDs
type FlowID int

// NilFlowID is nil value for flow IDs
const NilFlowID = FlowID(0)

// FlowType is the type for the type of a flow
type FlowType string

// flow type constants
const (
	FlowTypeMessaging  = FlowType("M")
	FlowTypeBackground = FlowType("B")
	FlowTypeVoice      = FlowType("V")
)

// Interrupts returns whether this flow type interrupts existing sessions
func (t FlowType) Interrupts() bool {
	return t != FlowTypeBackground
}

var flowTypeMapping = map[flows.FlowType]FlowType{
	flows.FlowTypeMessaging:           FlowTypeMessaging,
	flows.FlowTypeMessagingBackground: FlowTypeBackground,
	flows.FlowTypeVoice:               FlowTypeVoice,
}

// Flow is the mailroom type for a flow
type Flow struct {
	f struct {
		ID             FlowID          `json:"id"`
		OrgID          OrgID           `json:"org_id"`
		UUID           assets.FlowUUID `json:"uuid"`
		Name           string          `json:"name"`
		Version        string          `json:"version"`
		FlowType       FlowType        `json:"flow_type"`
		Definition     json.RawMessage `json:"definition"`
		IgnoreTriggers bool            `json:"ignore_triggers"`
		IVRRetry       int             `json:"ivr_retry"`
	}
}

// ID returns the ID for this flow
func (f *Flow) ID() FlowID { return f.f.ID }

// OrgID returns the Org ID for this flow
func (f *Flow) OrgID() OrgID { return f.f.OrgID }

// UUID returns the UUID for this flow
func (f *Flow) UUID() assets.FlowUUID { return f.f.UUID }

// Name returns the name of this flow
func (f *Flow) Name() string { return f.f.Name }

// Definition returns the definition for this flow
func (f *Flow) Definition() []byte { return f.f.Definition }

// FlowType return the type of flow this is
func (f *Flow) FlowType() FlowType { return f.f.FlowType }

// Version returns the version this flow was authored in
func (f *Flow) Version() string { return f.f.Version }

// IVRRetryWait returns the wait before retrying a failed IVR call (nil means no retry)
func (f *Flow) IVRRetryWait() *time.Duration {
	if f.f.IVRRetry == -1 { // never retry
		return nil
	} else if f.f.IVRRetry == 0 { // use default
		wait := CallRetryWait
		return &wait
	}

	wait := time.Minute * time.Duration(f.f.IVRRetry)
	return &wait
}

// IgnoreTriggers returns whether this flow ignores triggers
func (f *Flow) IgnoreTriggers() bool { return f.f.IgnoreTriggers }

// Reference return a flow reference for this flow
func (f *Flow) Reference() *assets.FlowReference {
	return assets.NewFlowReference(f.UUID(), f.Name())
}

// clones this flow but gives it the provided definition (used for simulation)
func (f *Flow) cloneWithNewDefinition(def []byte) *Flow {
	c := *f
	c.f.Definition = def
	return &c
}

func FlowIDForUUID(ctx context.Context, tx *sqlx.Tx, oa *OrgAssets, flowUUID assets.FlowUUID) (FlowID, error) {
	// first try to look up in our assets
	flow, _ := oa.FlowByUUID(flowUUID)
	if flow != nil {
		return flow.(*Flow).ID(), nil
	}

	// flow may be inactive, try to look up the ID only
	var flowID FlowID
	err := tx.GetContext(ctx, &flowID, `SELECT id FROM flows_flow WHERE org_id = $1 AND uuid = $2;`, oa.OrgID(), flowUUID)
	return flowID, err
}

func LoadFlowByUUID(ctx context.Context, db *sql.DB, orgID OrgID, flowUUID assets.FlowUUID) (*Flow, error) {
	return loadFlow(ctx, db, sqlSelectFlowByUUID, orgID, flowUUID)
}

func LoadFlowByName(ctx context.Context, db *sql.DB, orgID OrgID, name string) (*Flow, error) {
	return loadFlow(ctx, db, sqlSelectFlowByName, orgID, name)
}

func LoadFlowByID(ctx context.Context, db *sql.DB, orgID OrgID, flowID FlowID) (*Flow, error) {
	return loadFlow(ctx, db, sqlSelectFlowByID, orgID, flowID)
}

// loads the flow with the passed in UUID
func loadFlow(ctx context.Context, db *sql.DB, sql string, orgID OrgID, arg any) (*Flow, error) {
	start := time.Now()
	flow := &Flow{}

	rows, err := db.QueryContext(ctx, sql, orgID, arg)
	if err != nil {
		return nil, fmt.Errorf("error querying flow by: %v: %w", arg, err)
	}
	defer rows.Close()

	// no row, no flow!
	if !rows.Next() {
		return nil, nil
	}

	err = dbutil.ScanJSON(rows, &flow.f)
	if err != nil {
		return nil, fmt.Errorf("error reading flow definition by: %s: %w", arg, err)
	}

	slog.Debug("loaded flow", "elapsed", time.Since(start), "org_id", orgID, "flow", arg)

	return flow, nil
}

const baseSqlSelectFlow = `
SELECT ROW_TO_JSON(r) FROM (
	SELECT
		f.id, 
		f.org_id,
		f.uuid, 
		f.name,
		f.ignore_triggers,
		f.flow_type,
		fr.spec_version AS version,
		COALESCE(f.ivr_retry, 0) AS ivr_retry,
		definition::jsonb || 
			jsonb_build_object(
				'name', f.name,
				'uuid', f.uuid,
				'flow_type', f.flow_type,
				'expire_after_minutes', 
					CASE f.flow_type 
					WHEN 'M' THEN GREATEST(5, LEAST(f.expires_after_minutes, 20160))
					WHEN 'V' THEN GREATEST(1, LEAST(f.expires_after_minutes, 15))
					ELSE 0
					END,
				'metadata', jsonb_build_object(
					'uuid', f.uuid, 
					'id', f.id,
					'name', f.name,
					'revision', revision, 
					'expires', f.expires_after_minutes
				)
		) AS definition
	FROM
		flows_flow f
	INNER JOIN LATERAL (
		SELECT flow_id, spec_version, definition, revision
		FROM flows_flowrevision
		WHERE flow_id = f.id
		ORDER BY revision DESC
		LIMIT 1
	) fr ON fr.flow_id = f.id
	%s
) r;`

var sqlSelectFlowByUUID = fmt.Sprintf(baseSqlSelectFlow, `WHERE org_id = $1 AND uuid = $2 AND is_active = TRUE AND is_archived = FALSE`)
var sqlSelectFlowByName = fmt.Sprintf(baseSqlSelectFlow,
	`WHERE 
	    org_id = $1 AND LOWER(name) = LOWER($2) AND is_active = TRUE AND is_archived = FALSE 
	ORDER BY 
	    saved_on DESC LIMIT 1`,
)
var sqlSelectFlowByID = fmt.Sprintf(baseSqlSelectFlow, `WHERE org_id = $1 AND id = $2 AND is_active = TRUE AND is_archived = FALSE`)

func (i *FlowID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i FlowID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *FlowID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i FlowID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
