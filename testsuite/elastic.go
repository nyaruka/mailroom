package testsuite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/conflicts"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/require"
)

// Each test binary gets its own elastic indexes (suffixed with its process identifier), so concurrently
// running binaries - including other worktrees sharing an Elasticsearch - never see each other's documents.
// Within a binary, ClearElastic runs at the start of every test so each starts with empty indexes - which
// assumes tests run sequentially, as a t.Parallel() test would have its documents cleared by the next test
// to start. Ownership and sweeping of dead runs' indexes works as for all per-binary resources - see
// binary.go.

const (
	esContactsPrefix = "contacts-test-"
	esMessagesPrefix = "messages-test-"

	// name and index pattern base of the shared message index template, which covers the per-binary
	// message indexes of every binary
	esMessagesTemplate = "messages-test"
)

// per-binary index names
func esContactsIndex() string { return esContactsPrefix + binProcID() }
func esMessagesIndex() string { return esMessagesPrefix + binProcID() }

// setupElastic creates this binary's indexes and sweeps those of dead runs
func setupElastic(ctx context.Context, rt *runtime.Runtime) error {
	contactsBody, err := os.ReadFile(testdataPath("es_contacts.json"))
	if err != nil {
		return err
	}
	if _, err := rt.ES.Client.Indices.Create(esContactsIndex()).Raw(bytes.NewReader(contactsBody)).Do(ctx); err != nil {
		return fmt.Errorf("error creating contacts index: %w", err)
	}

	messagesBody, err := os.ReadFile(testdataPath("es_messages.json"))
	if err != nil {
		return err
	}
	messagesBody = bytes.ReplaceAll(messagesBody, []byte("{{INDEX}}"), []byte(esMessagesTemplate))
	if _, err := rt.ES.Client.Indices.PutIndexTemplate(esMessagesTemplate).Raw(bytes.NewReader(messagesBody)).Do(ctx); err != nil {
		return fmt.Errorf("error creating messages index template: %w", err)
	}

	return sweepStaleElastic(ctx, rt)
}

// sweepStaleElastic deletes the indexes of binaries which are no longer running
func sweepStaleElastic(ctx context.Context, rt *runtime.Runtime) error {
	indexes, err := rt.ES.Client.Cat.Indices().Index(esContactsPrefix + "*," + esMessagesPrefix + "*").Do(ctx)
	if err != nil {
		return fmt.Errorf("error listing test indexes: %w", err)
	}

	byProcID := make(map[string][]string)
	for _, idx := range indexes {
		if idx.Index == nil {
			continue
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(*idx.Index, esContactsPrefix), esMessagesPrefix)
		procID, _, _ := strings.Cut(rest, "-")
		byProcID[procID] = append(byProcID[procID], *idx.Index)
	}

	return sweepDeadBinaries(ctx, byProcID, func(name string) error {
		// unavailable is fine - another live binary's sweep can get there first
		if _, err := rt.ES.Client.Indices.Delete(name).IgnoreUnavailable(true).Do(ctx); err != nil {
			return fmt.Errorf("error deleting stale index %s: %w", name, err)
		}
		return nil
	})
}

// ClearElastic clears out this binary's elastic indexes: all documents from the contacts index, and the
// message indexes entirely. Runs at the start of every test, and can be called mid-test by tests which
// assert on exact index contents across phases.
func ClearElastic(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	rt.ES.Writer.Flush()

	// refresh so that recently written documents are visible to the delete query
	_, err := rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(t.Context())
	require.NoError(t, err)

	clearElasticContacts(t, rt)
	clearElasticMessages(t, rt)
}

// IndexContacts indexes all contacts for the test orgs into Elasticsearch. The index is cleared first so
// the result is exactly the indexable contacts in the database, regardless of what the test indexed before.
func IndexContacts(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	clearElasticContacts(t, rt)

	indexOrgContacts(t, rt, testdb.Org1)
	indexOrgContacts(t, rt, testdb.Org2)
}

// IndexMessages indexes all indexable messages from the database into Elasticsearch, then refreshes
// the index so they're immediately searchable. The message indexes are cleared first for the same reason
// IndexContacts clears the contacts index.
func IndexMessages(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	clearElasticMessages(t, rt)

	ctx := t.Context()

	const query = `
	SELECT m.uuid, m.org_id, m.text, m.created_on, m.ticket_uuid, c.uuid AS contact_uuid, COALESCE(u.path, '') AS urn_path
	  FROM msgs_msg m
	  JOIN contacts_contact c ON c.id = m.contact_id
	  LEFT JOIN contacts_contacturn u ON u.id = m.contact_urn_id
	 WHERE c.last_seen_on IS NOT NULL
	   AND LENGTH(m.text) >= $1
	   AND m.visibility NOT IN ('D', 'X')
	 ORDER BY m.uuid`

	rows, err := rt.DB.QueryContext(ctx, query, search.MessageTextMinLength)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var msgUUID, contactUUID, urnPath string
		var orgID models.OrgID
		var text string
		var createdOn time.Time
		var ticketUUID null.String

		err := rows.Scan(&msgUUID, &orgID, &text, &createdOn, &ticketUUID, &contactUUID, &urnPath)
		require.NoError(t, err)

		msg := search.MessageDoc{
			CreatedOn:   createdOn,
			UUID:        events.EventUUID(msgUUID),
			OrgID:       orgID,
			ContactUUID: core.ContactUUID(contactUUID),
			URNPath:     urnPath,
			Text:        text,
			InTicket:    ticketUUID != "",
		}

		rt.ES.Writer.Queue(&elastic.Document{
			Index:   msg.IndexName(rt.Config.ElasticMessagesIndex),
			ID:      string(msg.UUID),
			Routing: fmt.Sprintf("%d", msg.OrgID),
			Body:    jsonx.MustMarshal(msg),
		})
	}
	require.NoError(t, rows.Err())

	rt.ES.Writer.Flush()

	_, err = rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticMessagesIndex + "-*").Do(ctx)
	require.NoError(t, err)
}

// WriteMessageHistory writes the corresponding DynamoDB history events for all indexable messages in the database.
func WriteMessageHistory(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	ctx := t.Context()

	const query = `
	SELECT m.uuid, m.org_id, m.direction, m.text, m.created_on, c.uuid AS contact_uuid
	  FROM msgs_msg m
	  JOIN contacts_contact c ON c.id = m.contact_id
	 WHERE c.last_seen_on IS NOT NULL
	   AND LENGTH(m.text) >= $1
	   AND m.visibility NOT IN ('D', 'X')
	 ORDER BY m.uuid`

	rows, err := rt.DB.QueryContext(ctx, query, search.MessageTextMinLength)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var msgUUID, contactUUID string
		var orgID models.OrgID
		var direction, text string
		var createdOn time.Time

		err := rows.Scan(&msgUUID, &orgID, &direction, &text, &createdOn, &contactUUID)
		require.NoError(t, err)

		eventType := "msg_received"
		if direction == "O" {
			eventType = "msg_created"
		}

		item := &dynamo.Item{
			Key: dynamo.Key{
				PK: fmt.Sprintf("con#%s", contactUUID),
				SK: fmt.Sprintf("evt#%s", msgUUID),
			},
			OrgID: int(orgID),
			Data: map[string]any{
				"type":       eventType,
				"text":       text,
				"created_on": createdOn.Format(time.RFC3339),
			},
		}
		err = dynamo.PutItem(ctx, rt.Dynamo.History.Client(), rt.Dynamo.History.Table(), item)
		require.NoError(t, err)
	}
	require.NoError(t, rows.Err())
}

// IndexedMessage represents an indexed Elasticsearch message for test assertions, including metadata
// fields (_id and _routing) that aren't part of the document body.
type IndexedMessage struct {
	ID          string `json:"_id"`
	Routing     string `json:"_routing"`
	ContactUUID string `json:"contact_uuid"`
	URNPath     string `json:"urn_path,omitempty"`
	Text        string `json:"text"`
}

// GetIndexedMessages returns the documents currently in this binary's message indexes. It refreshes the
// indexes first so that writes which have already been applied are visible - but note that a refresh does
// not wait for an in-flight delete-by-query task, so after an operation which de-indexes asynchronously
// (e.g. search.DeindexMessagesByContact) use WaitForIndexedMessages instead.
func GetIndexedMessages(t *testing.T, rt *runtime.Runtime, clear bool) []IndexedMessage {
	t.Helper()

	rt.ES.Writer.Flush()

	pattern := rt.Config.ElasticMessagesIndex + "-*"

	// check if any message indexes exist
	indexes, err := rt.ES.Client.Cat.Indices().Index(pattern).Do(t.Context())
	require.NoError(t, err)

	if len(indexes) == 0 {
		return nil // no matching indexes yet
	}

	// refresh the indexes to make documents searchable
	_, err = rt.ES.Client.Indices.Refresh().Index(pattern).Do(t.Context())
	require.NoError(t, err)

	// search all documents
	results, err := rt.ES.Client.Search().Index(pattern).Raw(strings.NewReader(`{"query": {"match_all": {}}, "size": 1000}`)).Do(t.Context())
	require.NoError(t, err)

	msgs := make([]IndexedMessage, len(results.Hits.Hits))
	for i, hit := range results.Hits.Hits {
		err := json.Unmarshal(hit.Source_, &msgs[i])
		require.NoError(t, err)
		msgs[i].ID = *hit.Id_
		msgs[i].Routing = *hit.Routing_
	}

	slices.SortFunc(msgs, func(a, b IndexedMessage) int { return strings.Compare(a.ID, b.ID) })

	if clear {
		clearElasticMessages(t, rt)
	}

	return msgs
}

// WaitForIndexedMessages waits for this binary's message indexes to contain exactly count documents and
// returns them, failing the test if that doesn't happen within a few seconds. Use this rather than
// GetIndexedMessages when asserting on the result of an asynchronous de-index: Elastic runs a
// delete-by-query issued with wait_for_completion=false as a background task, so the documents can still
// be there when the request returns and refreshing the index doesn't wait for the task.
func WaitForIndexedMessages(t *testing.T, rt *runtime.Runtime, count int) []IndexedMessage {
	t.Helper()

	const timeout = 10 * time.Second
	const interval = 50 * time.Millisecond

	var msgs []IndexedMessage

	for deadline := time.Now().Add(timeout); ; {
		msgs = GetIndexedMessages(t, rt, false)
		if len(msgs) == count || time.Now().After(deadline) {
			break
		}
		time.Sleep(interval)
	}

	require.Len(t, msgs, count, "timed out waiting for message index to contain %d document(s)", count)

	return msgs
}

// SearchAssertion is a search query and the expected contact UUIDs that should match.
type SearchAssertion struct {
	Query    string             `json:"query"`
	Contacts []core.ContactUUID `json:"contacts"`
}

// removes all documents from the contacts index
func clearElasticContacts(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	_, err := rt.ES.Client.DeleteByQuery(rt.Config.ElasticContactsIndex).
		Conflicts(conflicts.Proceed).
		Raw(strings.NewReader(`{"query": {"match_all": {}}}`)).Do(t.Context())
	require.NoError(t, err)

	_, err = rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(t.Context())
	require.NoError(t, err)
}

// deletes all message indexes
func clearElasticMessages(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	pattern := rt.Config.ElasticMessagesIndex + "-*"

	indexes, err := rt.ES.Client.Cat.Indices().Index(pattern).Do(t.Context())
	require.NoError(t, err)

	for _, idx := range indexes {
		if idx.Index != nil {
			_, err := rt.ES.Client.Indices.Delete(*idx.Index).Do(t.Context())
			require.NoError(t, err)
		}
	}
}

// indexes all active contacts for the given org into Elastic and refreshes the index so they're immediately searchable
func indexOrgContacts(t *testing.T, rt *runtime.Runtime, org *testdb.Org) {
	t.Helper()

	ctx := t.Context()
	oa, err := models.GetOrgAssets(ctx, rt, org.ID)
	require.NoError(t, err)

	afterID := models.NilContactID
	for {
		contactIDs, err := models.GetContactIDsPage(ctx, rt.DB, org.ID, afterID, 10_000)
		require.NoError(t, err)

		if len(contactIDs) == 0 {
			break
		}

		mcs, err := models.LoadContacts(ctx, rt.DB, oa, contactIDs)
		require.NoError(t, err)

		contacts := make([]*core.Contact, 0, len(mcs))
		for _, mc := range mcs {
			contact, err := mc.EngineContact(oa)
			require.NoError(t, err)
			contacts = append(contacts, contact)
		}

		err = search.IndexContacts(ctx, rt, oa, contacts, map[models.ContactID]models.FlowID{})
		require.NoError(t, err)

		afterID = contactIDs[len(contactIDs)-1]
	}

	rt.ES.Writer.Flush()

	_, err = rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(ctx)
	require.NoError(t, err)
}
