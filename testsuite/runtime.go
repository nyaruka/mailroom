package testsuite

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path"
	goruntime "runtime"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/centrifugo"
	"github.com/nyaruka/mailroom/v26/core/goflow"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/require"
)

// testdataPath returns the absolute path of a file in this package's testdata directory. Paths are resolved relative
// to this source file rather than the module being tested, so that they also work from modules importing this package.
func testdataPath(file string) string {
	_, thisFile, _, _ := goruntime.Caller(0)
	return path.Join(path.Dir(thisFile), "testdata", file)
}

// Refresh is our type for the pieces of org assets we want fresh (not cached)
type ResetFlag int

// refresh bit masks
const (
	ResetNone    = ResetFlag(0)
	ResetAll     = ResetFlag(^0)
	ResetDB      = ResetFlag(1 << 1)
	ResetData    = ResetFlag(1 << 2)
	ResetValkey  = ResetFlag(1 << 3)
	ResetStorage = ResetFlag(1 << 4)
	ResetDynamo  = ResetFlag(1 << 5)
	ResetElastic = ResetFlag(1 << 6)
)

// Reset clears out both our database and redis DB
func Reset(t *testing.T, rt *runtime.Runtime, what ResetFlag) {
	if what&ResetValkey > 0 {
		resetValkey(t, rt)
	}
	if what&ResetStorage > 0 {
		resetStorage(t, rt)
	}
	if what&ResetDynamo > 0 {
		resetDynamo(t, rt)
	}

	// ResetDB and ResetData are no-ops - each test gets its own database (see dbtemplate.go) which is
	// dropped when it finishes, so there's nothing to reset

	// comes after data/db reset so reindexing happens from reset data
	if what&ResetElastic > 0 {
		resetElastic(t, rt)
	}
}

// Runtime returns the various runtime things a test might need
func Runtime(t *testing.T) (context.Context, *runtime.Runtime) {
	// each test gets its own database cloned from a template built from our dump - see dbtemplate.go
	dbName := createTestDB(t)
	t.Cleanup(func() { dropTestDB(t, dbName) })

	cfg := runtime.NewDefaultConfig()
	cfg.DeploymentID = "test"
	cfg.InternetPort = 8190
	cfg.InternalPort = 8191
	cfg.DB = fmt.Sprintf(dbTestDSNFormat, dbName)
	cfg.Valkey = "valkey://valkey:6379/10" // use a different DB from the default so a locally-running courier can't pop from queues we're asserting on

	// AWS SDK default chain reads these — used by the localstack S3/Dynamo/Cloudwatch clients
	t.Setenv("AWS_ACCESS_KEY_ID", "root")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "tembatemba")
	t.Setenv("AWS_REGION", "us-east-1")

	cfg.S3Endpoint = "http://localstack:4566"
	cfg.S3AttachmentsBucket = "test-attachments"
	cfg.S3PathStyle = true
	cfg.DynamoEndpoint = "http://localstack:4566"
	cfg.DynamoTablePrefix = "Test"
	cfg.ElasticContactsIndex = "contacts-test"
	cfg.ElasticMessagesIndex = "messages-test"
	cfg.SpoolDir = absPath("./_test_spool")

	err := cfg.Parse()
	require.NoError(t, err)

	rt, err := runtime.NewRuntime(cfg)
	require.NoError(t, err)

	createBucket(t, rt, rt.Config.S3AttachmentsBucket)
	setupElasticContacts(t, rt)
	setupElasticMessages(t, rt)

	// create Dynamo tables if necessary
	dyntest.CreateTables(t, rt.Dynamo.Main.Client(), testdataPath("dynamo.json"), false)

	rt.FCM = &MockFCMClient{ValidTokens: []string{"FCMID3", "FCMID4", "FCMID5"}}
	rt.Centrifugo = centrifugo.NewService(centrifugo.NewMockClient(), rt.VK)

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	err = rt.Start()
	require.NoError(t, err, "error starting runtime")

	models.InitCache(rt)

	t.Cleanup(func() {
		rt.Stop()

		rt.DB.Close()
		rt.VK.Close()

		goflow.Reset()

		models.FlushCache()
	})

	return t.Context(), rt
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

// resets our valkey database
func resetValkey(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	vc := rt.VK.Get()
	defer vc.Close()

	_, err := vc.Do("FLUSHDB")
	require.NoError(t, err, "error flushing valkey db")
}

func resetStorage(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	err := rt.S3.EmptyBucket(t.Context(), rt.Config.S3AttachmentsBucket)
	require.NoError(t, err)
}

// CentrifugoHistory returns the JSON payloads published to the given Centrifugo channel, oldest first. The runtime's
// Centrifugo client is a mock so this reads back what the test published rather than hitting a real server.
func CentrifugoHistory(t *testing.T, rt *runtime.Runtime, channel string) []json.RawMessage {
	t.Helper()

	var history []json.RawMessage
	for _, p := range rt.Centrifugo.Client.(*centrifugo.MockClient).Publications() {
		if p.Channel == channel {
			history = append(history, p.Data.(json.RawMessage)) // the mock records data as marshaled JSON
		}
	}
	return history
}

func createBucket(t *testing.T, rt *runtime.Runtime, bucket string) {
	t.Helper()

	err := rt.S3.Test(t.Context(), bucket)
	if err != nil {
		_, err = rt.S3.Client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(bucket)})
		require.NoError(t, err)
	}
}

func resetElastic(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	rt.ES.Writer.Flush()
	clearElasticIndexes(t, rt)
}

func resetDynamo(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	rt.Dynamo.Main.Flush()
	rt.Dynamo.History.Flush()

	dyntest.CreateTables(t, rt.Dynamo.Main.Client(), testdataPath("dynamo.json"), true)
}

func ReadFile(t *testing.T, path string) []byte {
	t.Helper()

	d, err := os.ReadFile(path)
	require.NoError(t, err)
	return d
}
