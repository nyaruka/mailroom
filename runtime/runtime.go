package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"firebase.google.com/go/v4/messaging"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/gomodule/redigo/redis"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/aws/cwatch"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/aws/s3x"
	"github.com/nyaruka/vkutil"
)

// Runtime represents the set of services required to run many Mailroom functions. Used as a wrapper for
// those services to simplify call signatures but not create a direct dependency to Mailroom or Server
type Runtime struct {
	Config *Config

	DB         *sqlx.DB
	ReadonlyDB *sql.DB
	Dynamo     *dynamodb.Client
	VK         *redis.Pool
	S3         *s3x.Service
	ES         *elasticsearch.TypedClient
	CW         *cwatch.Service
	FCM        FCMClient

	Writers *Writers
	Queues  *Queues
	Stats   *StatsCollector
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

	rt.Dynamo, err = dynamo.NewClient(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.DynamoEndpoint)
	if err != nil {
		return nil, fmt.Errorf("error creating DynamoDB client: %w", err)
	}

	rt.VK, err = vkutil.NewPool(cfg.Valkey)
	if err != nil {
		return nil, fmt.Errorf("error creating Valkey pool: %w", err)
	}

	rt.S3, err = s3x.NewService(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.S3Endpoint, cfg.S3Minio)
	if err != nil {
		return nil, fmt.Errorf("error creating S3 service: %w", err)
	}

	rt.ES, err = elasticsearch.NewTypedClient(elasticsearch.Config{Addresses: []string{cfg.Elastic}, Username: cfg.ElasticUsername, Password: cfg.ElasticPassword})
	if err != nil {
		return nil, fmt.Errorf("error creating Elasticsearch client: %w", err)
	}

	rt.CW, err = cwatch.NewService(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.CloudwatchNamespace, cfg.DeploymentID)
	if err != nil {
		return nil, fmt.Errorf("error creating Cloudwatch service: %w", err)
	}

	rt.Writers = NewWriters(cfg, rt.Dynamo)
	rt.Queues = NewQueues(cfg)
	rt.Stats = NewStatsCollector()

	return rt, nil
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
