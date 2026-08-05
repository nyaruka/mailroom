package testsuite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/require"
)

// Each test binary gets its own attachments bucket (suffixed with its process identifier), so concurrently
// running binaries never see each other's files. Within a binary, clearStorage runs at the start of every
// test so each starts with an empty bucket - assuming sequential tests, as elsewhere. Ownership and
// sweeping of dead runs' buckets works as for all per-binary resources - see binary.go.

const s3TestPrefix = "test-attachments-"

// per-binary bucket name
func s3AttachmentsBucket() string {
	return s3TestPrefix + binProcID()
}

// setupStorage creates this binary's attachments bucket and sweeps the buckets of dead runs
func setupStorage(ctx context.Context, rt *runtime.Runtime) error {
	if _, err := rt.S3.Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s3AttachmentsBucket())}); err != nil {
		return fmt.Errorf("error creating attachments bucket: %w", err)
	}

	return sweepStaleStorage(ctx, rt)
}

// sweepStaleStorage deletes the buckets of binaries which are no longer running
func sweepStaleStorage(ctx context.Context, rt *runtime.Runtime) error {
	resp, err := rt.S3.Client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return fmt.Errorf("error listing buckets: %w", err)
	}

	byProcID := make(map[string][]string)
	for _, b := range resp.Buckets {
		name := aws.ToString(b.Name)
		byProcID[strings.TrimPrefix(name, s3TestPrefix)] = []string{name}
	}

	return sweepDeadBinaries(ctx, byProcID, func(name string) error {
		// not found is fine - another live binary's sweep can get there first
		if err := rt.S3.EmptyBucket(ctx, name); err != nil && !isNoSuchBucket(err) {
			return fmt.Errorf("error emptying stale bucket %s: %w", name, err)
		}
		if _, err := rt.S3.Client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(name)}); err != nil && !isNoSuchBucket(err) {
			return fmt.Errorf("error deleting stale bucket %s: %w", name, err)
		}
		return nil
	})
}

// isNoSuchBucket returns whether the given error is S3 telling us the bucket doesn't exist
func isNoSuchBucket(err error) bool {
	var ae smithy.APIError
	return errors.As(err, &ae) && ae.ErrorCode() == "NoSuchBucket"
}

// clearStorage empties this binary's attachments bucket - runs at the start of every test
func clearStorage(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	err := rt.S3.EmptyBucket(t.Context(), rt.Config.S3AttachmentsBucket)
	require.NoError(t, err)
}
