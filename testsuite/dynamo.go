package testsuite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/require"
)

// Each test binary gets its own dynamo tables (prefixed with its process identifier), so concurrently
// running binaries never see each other's items. Within a binary, ClearDynamo runs at the start of every
// test so each starts with empty tables - assuming sequential tests, as elsewhere. Ownership is the same
// per-binary advisory lock that marks the elastic indexes (see dbtemplate.go), and setup sweeps away the
// tables of any run which has died.

// per-binary prefix for dynamo table names, e.g. Testa1b2c3d4 -> Testa1b2c3d4Main
func dynTablePrefix() string {
	return "Test" + dbProcID()
}

var dynSetup sync.Once
var dynSetupErr error

// ensureDynamo creates this binary's dynamo tables on first use
func ensureDynamo(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	dynSetup.Do(func() { dynSetupErr = setupDynamo(rt) })
	require.NoError(t, dynSetupErr, "error setting up dynamo tables")
}

// setupDynamo claims this binary's tables, creates them, and sweeps those of dead runs
func setupDynamo(rt *runtime.Runtime) error {
	ctx := context.Background()

	if err := claimBinary(); err != nil {
		return err
	}

	tablesJSON, err := os.ReadFile(testdataPath("dynamo.json"))
	if err != nil {
		return err
	}
	inputs := []*dynamodb.CreateTableInput{}
	if err := json.Unmarshal(tablesJSON, &inputs); err != nil {
		return fmt.Errorf("error unmarshaling dynamo.json: %w", err)
	}

	client := rt.Dynamo.Main.Client()

	for _, input := range inputs {
		input.TableName = aws.String(rt.Config.DynamoTablePrefix + *input.TableName)

		if _, err := client.CreateTable(ctx, input); err != nil {
			return fmt.Errorf("error creating table %s: %w", *input.TableName, err)
		}
	}

	// wait for tables to be usable - localstack creates synchronously but real DynamoDB doesn't
	waiter := dynamodb.NewTableExistsWaiter(client)
	for _, input := range inputs {
		if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: input.TableName}, 30*time.Second); err != nil {
			return fmt.Errorf("error waiting for table %s: %w", *input.TableName, err)
		}
	}

	return sweepStaleDynamo(ctx, client)
}

// sweepStaleDynamo deletes the tables of binaries which are no longer running - any process identifier
// whose per-binary lock we can take ourselves belongs to a run which is no longer around.
func sweepStaleDynamo(ctx context.Context, client *dynamodb.Client) error {
	admin, err := dbAdmin()
	if err != nil {
		return err
	}
	conn, err := admin.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	byProcID := make(map[string][]string)
	var startFrom *string
	for {
		resp, err := client.ListTables(ctx, &dynamodb.ListTablesInput{ExclusiveStartTableName: startFrom})
		if err != nil {
			return fmt.Errorf("error listing tables: %w", err)
		}
		for _, name := range resp.TableNames {
			rest := strings.TrimPrefix(name, "Test")
			if len(rest) < 8 {
				continue
			}
			procID := rest[:8]
			if procID == dbProcID() || !binProcIDRegex.MatchString(procID) {
				continue // ours, or not a per-binary test table
			}
			byProcID[procID] = append(byProcID[procID], name)
		}
		if resp.LastEvaluatedTableName == nil {
			break
		}
		startFrom = resp.LastEvaluatedTableName
	}

	for procID, names := range byProcID {
		var got bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, binKey(procID)).Scan(&got); err != nil {
			return err
		}
		if !got {
			continue // in use by a live run
		}

		for _, name := range names {
			// not found is fine - another live binary's sweep can get there first
			var notFound *dbtypes.ResourceNotFoundException
			if _, err := client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(name)}); err != nil && !errors.As(err, &notFound) {
				return fmt.Errorf("error deleting stale table %s: %w", name, err)
			}
		}

		conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, binKey(procID))
	}

	return nil
}

// ClearDynamo clears out this binary's dynamo tables. Runs at the start of every test, and can be called
// mid-test by tests which assert on exact table contents across phases.
func ClearDynamo(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	rt.Dynamo.Main.Flush()
	rt.Dynamo.History.Flush()

	dyntest.Truncate(t, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table())
	dyntest.Truncate(t, rt.Dynamo.History.Client(), rt.Dynamo.History.Table())
}

func GetHistoryItems(t *testing.T, rt *runtime.Runtime, clear bool, after time.Time) []*dynamo.Item {
	t.Helper()

	rt.Dynamo.History.Flush()

	table := rt.Dynamo.History.Table()

	allItems := dyntest.ScanAll(t, rt.Dynamo.History.Client(), table)

	if after.IsZero() {
		if clear {
			dyntest.Truncate(t, rt.Dynamo.History.Client(), table)
		}
		return allItems
	}

	// filter items by UUID7 time boundary
	afterMs := after.UnixMilli()
	items := make([]*dynamo.Item, 0, len(allItems))

	for _, item := range allItems {
		if skUUID7TimeMs(item.SK) >= afterMs {
			items = append(items, item)
		}
	}

	if clear && len(items) > 0 {
		client := rt.Dynamo.History.Client()
		for _, item := range items {
			_, err := client.DeleteItem(t.Context(), &dynamodb.DeleteItemInput{
				TableName: aws.String(table),
				Key: map[string]dbtypes.AttributeValue{
					"PK": &dbtypes.AttributeValueMemberS{Value: item.PK},
					"SK": &dbtypes.AttributeValueMemberS{Value: item.SK},
				},
			})
			require.NoError(t, err)
		}
	}

	return items
}

// skUUID7TimeMs extracts the millisecond timestamp from a sort key like "evt#<uuid7>" or "evt#<uuid7>#<tag>"
func skUUID7TimeMs(sk string) int64 {
	if len(sk) < 40 { // "evt#" (4) + UUID (36)
		return 0
	}
	// UUID7 first 48 bits = 12 hex chars at positions 0-7 and 9-12 of the UUID
	hex := sk[4:12] + sk[13:17]
	ms, err := strconv.ParseInt(hex, 16, 64)
	if err != nil {
		return 0
	}
	return ms
}

func GetHistoryEventTypes(t *testing.T, rt *runtime.Runtime, clear bool, after time.Time) map[core.ContactUUID][]string {
	items := GetHistoryItems(t, rt, clear, after)

	evtTypes := make(map[core.ContactUUID][]string, len(items))

	for _, item := range items {
		data, err := item.GetData()
		require.NoError(t, err)

		evtType, ok := data["type"]
		if ok {
			contactUUID := core.ContactUUID(item.PK)[4:] // trim off con#
			evtTypes[contactUUID] = append(evtTypes[contactUUID], evtType.(string))
		}
	}

	return evtTypes
}
