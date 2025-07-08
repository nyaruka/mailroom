package runtime

import (
	"context"
	"database/sql"

	"firebase.google.com/go/v4/messaging"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/gomodule/redigo/redis"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/aws/cwatch"
	"github.com/nyaruka/gocommon/aws/s3x"
)

// Runtime represents the set of services required to run many Mailroom functions. Used as a wrapper for
// those services to simplify call signatures but not create a direct dependency to Mailroom or Server
type Runtime struct {
	DB         *sqlx.DB
	ReadonlyDB *sql.DB
	VK         *redis.Pool
	Queues     *Queues
	Dynamo     *DynamoTables
	S3         *s3x.Service
	ES         *elasticsearch.TypedClient
	Stats      *StatsCollector
	CW         *cwatch.Service
	FCM        FCMClient
	Config     *Config
}

// FCMClient is an interface to allow mocking in tests
type FCMClient interface {
	Send(ctx context.Context, message ...*messaging.Message) (*messaging.BatchResponse, error)
}
