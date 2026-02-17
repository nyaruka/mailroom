package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"firebase.google.com/go/v4/messaging"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/gomodule/redigo/redis"
	awsx "github.com/nyaruka/gocommon/aws"
	"github.com/nyaruka/gocommon/aws/cwatch"
	"github.com/nyaruka/gocommon/aws/s3x"
	"github.com/nyaruka/vkutil"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/signer/awsv2"
	"github.com/vinovest/sqlx"
)

type OS struct {
	Messages *opensearchapi.Client
}

// Runtime represents the set of services required to run many Mailroom functions. Used as a wrapper for
// those services to simplify call signatures but not create a direct dependency to Mailroom or Server
type Runtime struct {
	Config *Config

	DB         *sqlx.DB
	ReadonlyDB *sql.DB
	VK         *redis.Pool
	S3         *s3x.Service
	ES         *elasticsearch.TypedClient
	OS         OS
	CW         *cwatch.Service
	FCM        FCMClient

	Dynamo *Dynamo
	Queues *Queues
	Stats  *StatsCollector
}

// FCMClient is an interface to allow mocking in tests
type FCMClient interface {
	Send(ctx context.Context, message ...*messaging.Message) (*messaging.BatchResponse, error)
}

func NewRuntime(cfg *Config) (*Runtime, error) {
	rt := &Runtime{Config: cfg}

	var err error

	rt.DB, err = createPostgresPool(cfg.DB, cfg.DBPoolSize)
	if err != nil {
		return nil, fmt.Errorf("error creating Postgres connection pool: %w", err)
	}

	if cfg.ReadonlyDB != "" {
		roDB, err := createPostgresPool(cfg.ReadonlyDB, cfg.DBPoolSize)
		if err != nil {
			return nil, fmt.Errorf("error creating readonly Postgres connection pool: %w", err)
		}

		rt.ReadonlyDB = roDB.DB
	} else {
		rt.ReadonlyDB = rt.DB.DB
	}

	rt.Dynamo, err = newDynamo(cfg)
	if err != nil {
		return nil, err
	}

	rt.VK, err = vkutil.NewPool(cfg.Valkey)
	if err != nil {
		return nil, fmt.Errorf("error creating Valkey pool: %w", err)
	}

	rt.S3, err = s3x.NewService(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.S3Endpoint, cfg.S3PathStyle)
	if err != nil {
		return nil, fmt.Errorf("error creating S3 service: %w", err)
	}

	rt.ES, err = elasticsearch.NewTypedClient(elasticsearch.Config{Addresses: []string{cfg.Elastic}, Username: cfg.ElasticUsername, Password: cfg.ElasticPassword})
	if err != nil {
		return nil, fmt.Errorf("error creating Elasticsearch client: %w", err)
	}

	if cfg.OpenSearchMessagesEndpoint != "" {
		rt.OS.Messages, err = createOpenSearchClient(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.OpenSearchMessagesEndpoint)
		if err != nil {
			return nil, fmt.Errorf("error creating OpenSearch messages service: %w", err)
		}
	}

	rt.CW, err = cwatch.NewService(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.CloudwatchNamespace, cfg.DeploymentID)
	if err != nil {
		return nil, fmt.Errorf("error creating Cloudwatch service: %w", err)
	}

	rt.Queues = newQueues(cfg)
	rt.Stats = NewStatsCollector()

	return rt, nil
}

func (r *Runtime) Start() error {
	if err := r.Dynamo.start(); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) Stop() {
	r.Dynamo.stop()
}

func createPostgresPool(url string, maxOpenConns int) (*sqlx.DB, error) {
	db, err := sqlx.Open("postgres", url)
	if err != nil {
		return nil, fmt.Errorf("unable to open database connection: '%s': %w", url, err)
	}

	db.SetMaxIdleConns(8)
	db.SetMaxOpenConns(maxOpenConns)
	db.SetConnMaxLifetime(time.Minute * 30)

	return db, nil
}

func createOpenSearchClient(accessKey, secretKey, region, url string) (*opensearchapi.Client, error) {
	awsCfg, err := awsx.NewConfig(accessKey, secretKey, region)
	if err != nil {
		return nil, fmt.Errorf("error creating AWS config: %w", err)
	}

	// AWS OpenSearch Serverless uses "aoss" as the service name for signing
	svc := "es"
	if strings.Contains(url, ".aoss.") {
		svc = "aoss"
	}

	signer, err := awsv2.NewSignerWithService(awsCfg, svc)
	if err != nil {
		return nil, fmt.Errorf("error creating request signer: %w", err)
	}

	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{Addresses: []string{url}, Signer: signer},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating client: %w", err)
	}
	return client, err
}
