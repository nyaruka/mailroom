package testsuite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/require"
)

// isNoSuchBucket returns whether the given error is S3 telling us the bucket doesn't exist
func isNoSuchBucket(err error) bool {
	var ae smithy.APIError
	return errors.As(err, &ae) && ae.ErrorCode() == "NoSuchBucket"
}

// Each test binary gets its own attachments bucket (suffixed with its process identifier), so concurrently
// running binaries never see each other's files. Within a binary, clearStorage runs at the start of every
// test so each starts with an empty bucket - assuming sequential tests, as elsewhere. Ownership is the same
// per-binary advisory lock that marks the elastic indexes and dynamo tables (see dbtemplate.go), and setup
// sweeps away the buckets of any run which has died.

const s3TestPrefix = "test-attachments-"

// per-binary bucket name
func s3AttachmentsBucket() string {
	return s3TestPrefix + dbProcID()
}

var s3Setup sync.Once
var s3SetupErr error

// ensureStorage creates this binary's attachments bucket on first use
func ensureStorage(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	s3Setup.Do(func() { s3SetupErr = setupStorage(rt) })
	require.NoError(t, s3SetupErr, "error setting up storage")
}

// setupStorage claims this binary's bucket, creates it, and sweeps those of dead runs
func setupStorage(rt *runtime.Runtime) error {
	ctx := context.Background()

	if err := claimBinary(); err != nil {
		return err
	}

	if _, err := rt.S3.Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s3AttachmentsBucket())}); err != nil {
		return fmt.Errorf("error creating attachments bucket: %w", err)
	}

	return sweepStaleStorage(ctx, rt)
}

// sweepStaleStorage deletes the buckets of binaries which are no longer running - any process identifier
// whose per-binary lock we can take ourselves belongs to a run which is no longer around.
func sweepStaleStorage(ctx context.Context, rt *runtime.Runtime) error {
	admin, err := dbAdmin()
	if err != nil {
		return err
	}
	conn, err := admin.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := rt.S3.Client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return fmt.Errorf("error listing buckets: %w", err)
	}

	for _, b := range resp.Buckets {
		name := aws.ToString(b.Name)
		procID := strings.TrimPrefix(name, s3TestPrefix)
		if procID == dbProcID() || !binProcIDRegex.MatchString(procID) {
			continue // ours, or not a per-binary test bucket
		}

		var got bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, binKey(procID)).Scan(&got); err != nil {
			return err
		}
		if !got {
			continue // in use by a live run
		}

		// not found is fine - another live binary's sweep can get there first
		if err := rt.S3.EmptyBucket(ctx, name); err != nil && !isNoSuchBucket(err) {
			return fmt.Errorf("error emptying stale bucket %s: %w", name, err)
		}
		if _, err := rt.S3.Client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(name)}); err != nil && !isNoSuchBucket(err) {
			return fmt.Errorf("error deleting stale bucket %s: %w", name, err)
		}

		conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, binKey(procID))
	}

	return nil
}

// clearStorage empties this binary's attachments bucket - runs at the start of every test
func clearStorage(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	err := rt.S3.EmptyBucket(t.Context(), rt.Config.S3AttachmentsBucket)
	require.NoError(t, err)
}
