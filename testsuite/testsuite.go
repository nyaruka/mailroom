package testsuite

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/gomodule/redigo/redis"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/aws/cwatch"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/aws/s3x"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/redisx/assertredis"
	"github.com/nyaruka/rp-indexer/v10/indexers"
	ixruntime "github.com/nyaruka/rp-indexer/v10/runtime"
)

var _db *sqlx.DB

const elasticURL = "http://localhost:9200"
const elasticContactsIndex = "test_contacts"
const postgresContainerName = "textit-postgres-1"

// Refresh is our type for the pieces of org assets we want fresh (not cached)
type ResetFlag int

// refresh bit masks
const (
	ResetAll     = ResetFlag(^0)
	ResetDB      = ResetFlag(1 << 1)
	ResetData    = ResetFlag(1 << 2)
	ResetRedis   = ResetFlag(1 << 3)
	ResetStorage = ResetFlag(1 << 4)
	ResetElastic = ResetFlag(1 << 5)
	ResetDynamo  = ResetFlag(1 << 6)
)

// Reset clears out both our database and redis DB
func Reset(what ResetFlag) {
	ctx, rt := Runtime() // TODO pass rt from test?

	if what&ResetDB > 0 {
		resetDB()
	} else if what&ResetData > 0 {
		resetData()
	}
	if what&ResetRedis > 0 {
		resetRedis()
	}
	if what&ResetStorage > 0 {
		resetStorage(ctx, rt)
	}
	if what&ResetElastic > 0 {
		resetElastic(ctx, rt)
	}
	if what&ResetDynamo > 0 {
		resetDynamo(ctx, rt)
	}

	models.FlushCache()
}

// Runtime returns the various runtime things a test might need
func Runtime() (context.Context, *runtime.Runtime) {
	cfg := runtime.NewDefaultConfig()
	cfg.DeploymentID = "test"
	cfg.Port = 8091
	cfg.ElasticContactsIndex = elasticContactsIndex
	cfg.AWSAccessKeyID = "root"
	cfg.AWSSecretAccessKey = "tembatemba"
	cfg.S3Endpoint = "http://localhost:9000"
	cfg.S3AttachmentsBucket = "test-attachments"
	cfg.S3SessionsBucket = "test-sessions"
	cfg.S3Minio = true
	cfg.DynamoEndpoint = "http://localhost:6000"
	cfg.DynamoTablePrefix = "Test"

	dyna, err := dynamo.NewService(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.DynamoEndpoint, cfg.DynamoTablePrefix)
	noError(err)

	s3svc, err := s3x.NewService(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.S3Endpoint, cfg.S3Minio)
	noError(err)

	cwSvc, err := cwatch.NewService(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.AWSRegion, cfg.CloudwatchNamespace, cfg.DeploymentID)
	noError(err)

	dbx := getDB()
	rt := &runtime.Runtime{
		DB:         dbx,
		ReadonlyDB: dbx.DB,
		RP:         getRP(),
		Dynamo:     dyna,
		S3:         s3svc,
		ES:         getES(),
		CW:         cwSvc,
		Stats:      runtime.NewStatsCollector(),
		FCM:        &MockFCMClient{ValidTokens: []string{"FCMID3", "FCMID4", "FCMID5"}},
		Config:     cfg,
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	return context.Background(), rt
}

// reindexes data changes to Elastic
func ReindexElastic(ctx context.Context) {
	db := getDB()
	es := getES()

	contactsIndexer := indexers.NewContactIndexer(elasticURL, elasticContactsIndex, 1, 1, 100)
	contactsIndexer.Index(&ixruntime.Runtime{DB: db.DB}, false, false)

	_, err := es.Indices.Refresh().Index(elasticContactsIndex).Do(ctx)
	noError(err)
}

// returns an open test database pool
func getDB() *sqlx.DB {
	if _db == nil {
		_db = sqlx.MustOpen("postgres", "postgres://mailroom_test:temba@localhost/mailroom_test?sslmode=disable&Timezone=UTC")

		// check if we have tables and if not load test database dump
		_, err := _db.Exec("SELECT * from orgs_org")
		if err != nil {
			loadTestDump()
			return getDB()
		}
	}
	return _db
}

// returns a redis pool to our test database
func getRP() *redis.Pool {
	return assertredis.TestDB()
}

// returns a redis connection, Close() should be called on it when done
func getRC() redis.Conn {
	conn, err := redis.Dial("tcp", "localhost:6379")
	noError(err)
	_, err = conn.Do("SELECT", 0)
	noError(err)
	return conn
}

// returns an Elastic client
func getES() *elasticsearch.TypedClient {
	es, err := elasticsearch.NewTypedClient(elasticsearch.Config{Addresses: []string{elasticURL}})
	noError(err)
	return es
}

// resets our database to our base state from our RapidPro dump
//
// mailroom_test.dump can be regenerated by running:
//
//	% python manage.py mailroom_db
//
// then copying the mailroom_test.dump file to your mailroom root directory
//
//	% cp mailroom_test.dump ../mailroom
func resetDB() {
	db := getDB()
	db.MustExec("DROP OWNED BY mailroom_test CASCADE")

	loadTestDump()
}

func loadTestDump() {
	dump, err := os.Open(absPath("./testsuite/testfiles/postgres.dump"))
	must(err)
	defer dump.Close()

	cmd := exec.Command("docker", "exec", "-i", postgresContainerName, "pg_restore", "-d", "mailroom_test", "-U", "mailroom_test")
	cmd.Stdin = dump

	output, err := cmd.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("error restoring database: %s: %s", err, string(output)))
	}

	// force re-connection
	if _db != nil {
		_db.Close()
		_db = nil
	}
}

// Converts a project root relative path to an absolute path usable in any test. This is needed because go tests
// are run with a working directory set to the current module being tested.
func absPath(p string) string {
	// start in working directory and go up until we are in a directory containing go.mod
	dir, _ := os.Getwd()
	for dir != "/" {
		if _, err := os.Stat(path.Join(dir, "go.mod")); err == nil {
			break
		}
		dir = path.Dir(dir)
	}
	return path.Join(dir, p)
}

// resets our redis database
func resetRedis() {
	assertredis.FlushDB()
}

func resetStorage(ctx context.Context, rt *runtime.Runtime) {
	rt.S3.EmptyBucket(ctx, rt.Config.S3AttachmentsBucket)
	rt.S3.EmptyBucket(ctx, rt.Config.S3SessionsBucket)
}

// clears indexed data in Elastic
func resetElastic(ctx context.Context, rt *runtime.Runtime) {
	exists, err := rt.ES.Indices.ExistsAlias(elasticContactsIndex).Do(ctx)
	noError(err)

	if exists {
		// get any indexes for the contacts alias
		ar, err := rt.ES.Indices.GetAlias().Name(elasticContactsIndex).Do(ctx)
		noError(err)

		// and delete them
		for index := range maps.Keys(ar) {
			_, err := rt.ES.Indices.Delete(index).Do(ctx)
			noError(err)
		}
	}

	ReindexElastic(ctx)
}

func resetDynamo(ctx context.Context, rt *runtime.Runtime) {
	tablesFile, err := os.Open(absPath("./testsuite/testfiles/dynamo.json"))
	must(err)
	defer tablesFile.Close()

	tablesJSON, err := io.ReadAll(tablesFile)
	must(err)

	inputs := []*dynamodb.CreateTableInput{}
	jsonx.MustUnmarshal(tablesJSON, &inputs)

	for _, input := range inputs {
		input.TableName = aws.String(rt.Dynamo.TableName(*input.TableName))

		// delete table if it exists
		if _, err := rt.Dynamo.Client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: input.TableName}); err == nil {
			_, err := rt.Dynamo.Client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: input.TableName})
			must(err)
		}

		_, err := rt.Dynamo.Client.CreateTable(ctx, input)
		must(err)
	}
}

var sqlResetTestData = `
UPDATE contacts_contact SET last_seen_on = NULL, current_session_uuid = NULL, current_flow_id = NULL;

DELETE FROM tickets_ticketdailycount;
DELETE FROM tickets_ticketdailytiming;
DELETE FROM notifications_notification;
DELETE FROM notifications_incident;
DELETE FROM request_logs_httplog;
DELETE FROM tickets_ticketdailycount;
DELETE FROM tickets_ticketevent;
DELETE FROM tickets_ticket;
DELETE FROM triggers_trigger_contacts WHERE trigger_id >= 30000;
DELETE FROM triggers_trigger_groups WHERE trigger_id >= 30000;
DELETE FROM triggers_trigger_exclude_groups WHERE trigger_id >= 30000;
DELETE FROM triggers_trigger WHERE id >= 30000;
DELETE FROM channels_channel WHERE id >= 30000;
DELETE FROM channels_channelcount;
DELETE FROM channels_channelevent;
DELETE FROM channels_channellog;
DELETE FROM msgs_msg;
DELETE FROM flows_flowrun;
DELETE FROM flows_flowactivitycount;
DELETE FROM flows_flowresultcount;
DELETE FROM flows_flowstartcount;
DELETE FROM flows_flowstart_contacts;
DELETE FROM flows_flowstart_groups;
DELETE FROM flows_flowstart;
DELETE FROM flows_flowsession;
DELETE FROM flows_flowrevision WHERE flow_id >= 30000;
DELETE FROM flows_flow WHERE id >= 30000;
DELETE FROM ivr_call;
DELETE FROM msgs_msg_labels;
DELETE FROM msgs_msg;
DELETE FROM msgs_broadcast_groups;
DELETE FROM msgs_broadcast_contacts;
DELETE FROM msgs_broadcastmsgcount;
DELETE FROM msgs_broadcast;
DELETE FROM msgs_optin;
DELETE FROM templates_templatetranslation WHERE id >= 30000;
DELETE FROM templates_template WHERE id >= 30000;
DELETE FROM schedules_schedule;
DELETE FROM campaigns_campaignevent WHERE id >= 30000;
DELETE FROM campaigns_campaign WHERE id >= 30000;
DELETE FROM contacts_contactfire;
DELETE FROM contacts_contactimportbatch;
DELETE FROM contacts_contactimport;
DELETE FROM contacts_contacturn WHERE id >= 30000;
DELETE FROM contacts_contactgroup_contacts WHERE contact_id >= 30000 OR contactgroup_id >= 30000;
DELETE FROM contacts_contact WHERE id >= 30000;
DELETE FROM contacts_contactgroupcount WHERE group_id >= 30000;
DELETE FROM contacts_contactgroup WHERE id >= 30000;
DELETE FROM orgs_itemcount;
DELETE FROM orgs_dailycount;

ALTER SEQUENCE flows_flow_id_seq RESTART WITH 30000;
ALTER SEQUENCE tickets_ticket_id_seq RESTART WITH 1;
ALTER SEQUENCE channels_channelevent_id_seq RESTART WITH 1;
ALTER SEQUENCE msgs_msg_id_seq RESTART WITH 1;
ALTER SEQUENCE msgs_broadcast_id_seq RESTART WITH 1;
ALTER SEQUENCE flows_flowrun_id_seq RESTART WITH 1;
ALTER SEQUENCE flows_flowstart_id_seq RESTART WITH 1;
ALTER SEQUENCE flows_flowsession_id_seq RESTART WITH 1;
ALTER SEQUENCE contacts_contact_id_seq RESTART WITH 30000;
ALTER SEQUENCE contacts_contacturn_id_seq RESTART WITH 30000;
ALTER SEQUENCE contacts_contactgroup_id_seq RESTART WITH 30000;
ALTER SEQUENCE campaigns_campaign_id_seq RESTART WITH 30000;
ALTER SEQUENCE campaigns_campaignevent_id_seq RESTART WITH 30000;`

// removes contact data not in the test database dump. Note that this function can't
// undo changes made to the contact data in the test database dump.
func resetData() {
	db := getDB()
	db.MustExec(sqlResetTestData)

	// because groups have changed
	models.FlushCache()
}

// convenience way to call a func and panic if it errors, e.g. must(foo())
func must(err error) {
	if err != nil {
		panic(err)
	}
}

// if just checking an error is nil noError(err) reads better than must(err)
var noError = must

func ReadFile(path string) []byte {
	d, err := os.ReadFile(path)
	noError(err)
	return d
}
