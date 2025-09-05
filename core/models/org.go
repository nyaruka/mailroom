package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/engine"
	"github.com/nyaruka/goflow/services/email/smtp"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/goflow/utils/smtpx"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/services/airtime/dtone"
	"github.com/nyaruka/null/v3"
)

// Register a airtime service factory with the engine
func init() {
	goflow.RegisterEmailServiceFactory(emailServiceFactory)
	goflow.RegisterAirtimeServiceFactory(airtimeServiceFactory)
}

func emailServiceFactory(rt *runtime.Runtime) engine.EmailServiceFactory {
	var emailRetries = smtpx.NewFixedRetries(time.Second*3, time.Second*6)

	return func(sa flows.SessionAssets) (flows.EmailService, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()

		return orgFromAssets(sa).EmailService(ctx, rt, emailRetries)
	}
}

func airtimeServiceFactory(rt *runtime.Runtime) engine.AirtimeServiceFactory {
	// give airtime transfers an extra long timeout
	airtimeHTTPClient := &http.Client{Timeout: time.Duration(120 * time.Second)}
	airtimeHTTPRetries := httpx.NewFixedRetries(time.Second*5, time.Second*10)

	return func(sa flows.SessionAssets) (flows.AirtimeService, error) {
		return orgFromAssets(sa).AirtimeService(airtimeHTTPClient, airtimeHTTPRetries)
	}
}

// OrgID is our type for org ids
type OrgID int

func (i OrgID) String() string {
	return strconv.FormatInt(int64(i), 10)
}

// OrgUUID is our type for org UUIDs
type OrgUUID uuids.UUID

const (
	// NilOrgID is the id 0 considered as nil org id
	NilOrgID = OrgID(0)

	configDTOneKey    = "dtone_key"
	configDTOneSecret = "dtone_secret"
)

// Org is mailroom's type for RapidPro orgs. It also implements the envs.Environment interface for GoFlow
type Org struct {
	o struct {
		UUID            OrgUUID       `json:"uuid"`
		ID              OrgID         `json:"id"`
		ParentID        OrgID         `json:"parent_id"`
		Name            string        `json:"name"`
		Suspended       bool          `json:"is_suspended"`
		FlowSMTP        null.String   `json:"flow_smtp"`
		PrometheusToken null.String   `json:"prometheus_token"`
		Config          null.Map[any] `json:"config"`
	}
	env envs.Environment
}

// Environment adds values from config
type Environment struct {
	envs.Environment

	obfuscationKey [4]uint32
}

func newEnvironment(base envs.Environment, cfg *runtime.Config) envs.Environment {
	return &Environment{
		Environment:    base,
		obfuscationKey: cfg.ParseIDObfuscationKey(),
	}
}

func (e *Environment) ObfuscationKey() [4]uint32 { return e.obfuscationKey }

// ID returns the id of the org
func (o *Org) ID() OrgID { return o.o.ID }

// Name returns the name of the org
func (o *Org) Name() string { return o.o.Name }

// Suspended returns whether the org has been suspended
func (o *Org) Suspended() bool { return o.o.Suspended }

// FlowSMTP provides custom SMTP settings for flow sessions
func (o *Org) FlowSMTP() string { return string(o.o.FlowSMTP) }

// FlowSMTP provides custom SMTP settings for flow sessions
func (o *Org) PrometheusToken() string { return string(o.o.PrometheusToken) }

// Environment returns this org as an engine environment
func (o *Org) Environment() envs.Environment { return o.env }

// MarshalJSON is our custom marshaller so that our inner env get output
func (o *Org) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.env)
}

// UnmarshalJSON is our custom unmarshaller
func (o *Org) UnmarshalJSON(b []byte) error {
	err := jsonx.Unmarshal(b, &o.o)
	if err != nil {
		return err
	}

	o.env, err = envs.ReadEnvironment(b)
	if err != nil {
		return err
	}

	return nil
}

// ConfigValue returns the string value for the passed in config (or default if not found)
func (o *Org) ConfigValue(key string, def string) string {
	v, ok := o.o.Config[key].(string)
	if ok {
		return v
	}
	return def
}

// EmailService returns the email service for this org
func (o *Org) EmailService(ctx context.Context, rt *runtime.Runtime, retries *smtpx.RetryConfig) (flows.EmailService, error) {
	// first look for custom SMTP on this org
	smtpURL := o.FlowSMTP()

	// secondly look on parent org if there is one
	if smtpURL == "" && o.o.ParentID != NilOrgID {
		parent, err := GetOrgAssets(ctx, rt, o.o.ParentID)
		if err != nil {
			return nil, fmt.Errorf("error loading parent org: %w", err)
		}
		smtpURL = parent.Org().FlowSMTP()
	}

	// finally use config default
	if smtpURL == "" {
		smtpURL = rt.Config.SMTPServer
	}

	if smtpURL == "" {
		return nil, errors.New("missing SMTP configuration")
	}

	return smtp.NewService(smtpURL, retries)
}

// AirtimeService returns the airtime service for this org if one is configured
func (o *Org) AirtimeService(httpClient *http.Client, httpRetries *httpx.RetryConfig) (flows.AirtimeService, error) {
	key := o.ConfigValue(configDTOneKey, "")
	secret := o.ConfigValue(configDTOneSecret, "")

	if key == "" || secret == "" {
		return nil, fmt.Errorf("missing %s or %s on DTOne configuration for org: %d", configDTOneKey, configDTOneSecret, o.ID())
	}
	return dtone.NewService(httpClient, httpRetries, key, secret), nil
}

// StoreAttachment saves an attachment to storage
func (o *Org) StoreAttachment(ctx context.Context, rt *runtime.Runtime, filename string, contentType string, content io.ReadCloser) (utils.Attachment, error) {
	// read the content
	contentBytes, err := io.ReadAll(content)
	if err != nil {
		return "", fmt.Errorf("unable to read attachment content: %w", err)
	}
	content.Close()

	if contentType == "" {
		contentType, _ = httpx.DetectContentType(contentBytes)
		contentType, _, _ = mime.ParseMediaType(contentType)
	}

	path := o.attachmentPath("attachments", filename)

	url, err := rt.S3.PutObject(ctx, rt.Config.S3AttachmentsBucket, path, contentType, contentBytes, types.ObjectCannedACLPublicRead)
	if err != nil {
		return "", fmt.Errorf("unable to store attachment content: %w", err)
	}

	return utils.Attachment(contentType + ":" + url), nil
}

func (o *Org) attachmentPath(prefix string, filename string) string {
	parts := []string{prefix, fmt.Sprintf("%d", o.ID())}

	// not all filesystems like having a directory with a huge number of files, so if filename is long enough,
	// use parts of it to create intermediate subdirectories
	if len(filename) > 4 {
		parts = append(parts, filename[:4])

		if len(filename) > 8 {
			parts = append(parts, filename[4:8])
		}
	}
	parts = append(parts, filename)

	return filepath.Join(parts...)
}

// gets the underlying org for the given session assets
func orgFromAssets(sa flows.SessionAssets) *Org {
	return sa.Source().(*OrgAssets).Org()
}

const sqlSelectOrgByID = `
SELECT ROW_TO_JSON(o) FROM (SELECT
	uuid,
	id,
	parent_id,
	name,
	is_suspended,
	flow_smtp,
	prometheus_token,
	o.config AS config,
	(SELECT CASE date_format WHEN 'D' THEN 'DD-MM-YYYY' WHEN 'M' THEN 'MM-DD-YYYY' ELSE 'YYYY-MM-DD' END) AS date_format, 
	'tt:mm' AS time_format,
	timezone,
	(SELECT CASE is_anon WHEN TRUE THEN 'urns' WHEN FALSE THEN 'none' END) AS redaction_policy,
	flow_languages AS allowed_languages,
	input_collation,
	COALESCE(
		(
			SELECT country FROM channels_channel c
			WHERE c.org_id = o.id AND c.is_active = TRUE AND c.country IS NOT NULL
			GROUP BY c.country ORDER BY count(c.country) desc, country LIMIT 1
	    ), ''
	) AS default_country
	FROM orgs_org o
	WHERE id = $1
) o`

// LoadOrg loads the org for the passed in id, returning any error encountered
func LoadOrg(ctx context.Context, cfg *runtime.Config, db *sql.DB, orgID OrgID) (*Org, error) {
	o := &Org{}
	rows, err := db.QueryContext(ctx, sqlSelectOrgByID, orgID)
	if err != nil {
		return nil, fmt.Errorf("error loading org: %d: %w", orgID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, fmt.Errorf("no org with id: %d", orgID)
	}

	if err := dbutil.ScanJSON(rows, o); err != nil {
		return nil, fmt.Errorf("error unmarshalling org: %w", err)
	}

	o.env = newEnvironment(o.env, cfg)
	return o, nil
}

// GetOrgIDFromUUID gets an org ID from a UUID (returns NilOrgID if not found)
func GetOrgIDFromUUID(ctx context.Context, db *sql.DB, orgUUID OrgUUID) (OrgID, error) {
	var orgID OrgID
	err := db.QueryRowContext(ctx, `SELECT id FROM orgs_org WHERE is_active = TRUE AND uuid = $1`, orgUUID).Scan(&orgID)
	if err != nil && err != sql.ErrNoRows {
		return NilOrgID, fmt.Errorf("error getting org id by uuid: %w", err)
	}

	return orgID, nil
}

const sqlSelectOutboxCounts = `
  SELECT org_id, SUM(count) 
    FROM orgs_itemcount 
   WHERE org_id = ANY($1) AND scope = 'msgs:folder:O' 
GROUP BY org_id, scope`

// GetOutboxCounts returns a map of org IDs to their outbox counts
func GetOutboxCounts(ctx context.Context, db *sql.DB, orgIDs []OrgID) (map[OrgID]int, error) {
	counts := make(map[OrgID]int)
	if len(orgIDs) == 0 {
		return counts, nil
	}

	rows, err := db.QueryContext(ctx, sqlSelectOutboxCounts, pq.Array(orgIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if err := dbutil.ScanAllMap(rows, counts); err != nil {
		return nil, err
	}
	return counts, nil
}
