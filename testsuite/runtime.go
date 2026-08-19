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

	// this binary's slot gives it its own valkey database and web server ports - see slot.go
	slot := claimSlot(t)

	cfg := runtime.NewDefaultConfig()
	cfg.DeploymentID = "test"
	cfg.InternetPort = slotPortBase + 2*slot
	cfg.InternalPort = cfg.InternetPort + 1
	cfg.DB = fmt.Sprintf(dbTestDSNFormat, dbName)

	// a hard ceiling, not a hint - a test needing more concurrent connections will block on the pool -
	// but tests need few, and concurrent binaries must share the server's connection limit
	cfg.DBPoolSize = 8

	cfg.Valkey = fmt.Sprintf(vkTestDSNFormat, slotVKDB(slot))
	cfg.ElasticContactsIndex = esContactsIndex() // this binary's own indexes, cleared before every test
	cfg.ElasticMessagesIndex = esMessagesIndex() // - see elastic.go

	// AWS SDK default chain reads these — used by the S3/Dynamo/Cloudwatch clients
	t.Setenv("AWS_ACCESS_KEY_ID", "root")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "tembatemba")
	t.Setenv("AWS_REGION", "us-east-1")

	cfg.S3Endpoint = "http://localstack:4566"
	cfg.S3AttachmentsBucket = s3AttachmentsBucket() // this binary's own bucket, emptied before every test - see storage.go
	cfg.S3PathStyle = true
	cfg.DynamoEndpoint = "http://dynamodb:8000"
	cfg.DynamoTablePrefix = dynTablePrefix() // this binary's own tables, cleared before every test - see dynamo.go
	cfg.SpoolDir = t.TempDir()

	err := cfg.Parse()
	require.NoError(t, err)

	rt, err := runtime.NewRuntime(cfg)
	require.NoError(t, err)

	ensureBinaryResources(t, rt) // creates those on first use and sweeps dead runs' - see binary.go

	rt.FCM = &MockFCMClient{ValidTokens: []string{"FCMID3", "FCMID4", "FCMID5"}}
	rt.Embeddings = &MockEmbedder{} // tests needing particular vectors replace this with their own
	rt.Centrifugo = centrifugo.NewService(centrifugo.NewMockClient(), rt.VK)

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	err = rt.Start()
	require.NoError(t, err, "error starting runtime")

	// so every test starts with empty valkey, indexes, tables and storage (writers must be started for
	// their flushes)
	require.NoError(t, flushVKDB(slotVKDB(slot)))
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
