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

// Runtime returns the various runtime things a test might need
func Runtime(t *testing.T) (context.Context, *runtime.Runtime) {
	// each test gets its own database cloned from a template built from our dump - see dbtemplate.go
	dbName := createTestDB(t)
	t.Cleanup(func() { dropTestDB(t, dbName) })

	// and its own valkey database claimed from the server's pool - see valkey.go
	vkDB := claimVKDB(t)

	cfg := runtime.NewDefaultConfig()
	cfg.DeploymentID = "test"

	// web server ports are derived from the valkey claim, which is already unique per running test, so
	// tests in concurrently running binaries never contend for a port
	cfg.InternetPort = 8200 + 2*(vkDB-vkTestDBMin)
	cfg.InternalPort = cfg.InternetPort + 1

	cfg.DB = fmt.Sprintf(dbTestDSNFormat, dbName)
	cfg.DBPoolSize = 8 // tests need few connections, and concurrent binaries must share the server's limit
	cfg.Valkey = fmt.Sprintf(vkTestDSNFormat, vkDB)
	cfg.ElasticContactsIndex = esContactsIndex() // this binary's own indexes, cleared before every test
	cfg.ElasticMessagesIndex = esMessagesIndex() // - see elastic.go

	// AWS SDK default chain reads these — used by the localstack S3/Dynamo/Cloudwatch clients
	t.Setenv("AWS_ACCESS_KEY_ID", "root")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "tembatemba")
	t.Setenv("AWS_REGION", "us-east-1")

	cfg.S3Endpoint = "http://localstack:4566"
	cfg.S3AttachmentsBucket = s3AttachmentsBucket() // this binary's own bucket, emptied before every test - see storage.go
	cfg.S3PathStyle = true
	cfg.DynamoEndpoint = "http://localstack:4566"
	cfg.DynamoTablePrefix = dynTablePrefix() // this binary's own tables, cleared before every test - see dynamo.go
	cfg.SpoolDir = absPath("./_test_spool/" + dbProcID())

	err := cfg.Parse()
	require.NoError(t, err)

	rt, err := runtime.NewRuntime(cfg)
	require.NoError(t, err)

	ensureStorage(t, rt)
	ensureElastic(t, rt)
	ensureDynamo(t, rt)

	rt.FCM = &MockFCMClient{ValidTokens: []string{"FCMID3", "FCMID4", "FCMID5"}}
	rt.Centrifugo = centrifugo.NewService(centrifugo.NewMockClient(), rt.VK)

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	err = rt.Start()
	require.NoError(t, err, "error starting runtime")

	// so every test starts with empty indexes, tables and storage (writers must be started for their flushes)
	ClearElastic(t, rt)
	ClearDynamo(t, rt)
	clearStorage(t, rt)

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

func ReadFile(t *testing.T, path string) []byte {
	t.Helper()

	d, err := os.ReadFile(path)
	require.NoError(t, err)
	return d
}
