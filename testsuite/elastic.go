package testsuite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/conflicts"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/require"
)

// IndexContacts indexes all contacts for the test orgs into Elasticsearch.
func IndexContacts(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	indexOrgContacts(t, rt, testdb.Org1)
	indexOrgContacts(t, rt, testdb.Org2)
}

// IndexMessages indexes all indexable messages from the database into Elasticsearch, then refreshes
// the index so they're immediately searchable.
func IndexMessages(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	ctx := t.Context()

	const query = `
	SELECT m.uuid, m.org_id, m.text, m.created_on, m.ticket_uuid, c.uuid AS contact_uuid
	  FROM msgs_msg m
	  JOIN contacts_contact c ON c.id = m.contact_id
	 WHERE (m.direction = 'I' OR (m.broadcast_id IS NULL AND m.created_by_id IS NOT NULL))
	   AND LENGTH(m.text) >= $1
	   AND m.visibility IN ('V', 'A')
	   AND m.msg_type != 'V'
	 ORDER BY m.uuid`

	rows, err := rt.DB.QueryContext(ctx, query, search.MessageTextMinLength)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var msgUUID, contactUUID string
		var orgID models.OrgID
		var text string
		var createdOn time.Time
		var ticketUUID null.String

		err := rows.Scan(&msgUUID, &orgID, &text, &createdOn, &ticketUUID, &contactUUID)
		require.NoError(t, err)

		msg := search.MessageDoc{
			CreatedOn:   createdOn,
			UUID:        flows.EventUUID(msgUUID),
			OrgID:       orgID,
			ContactUUID: flows.ContactUUID(contactUUID),
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
	 WHERE (m.direction = 'I' OR (m.broadcast_id IS NULL AND m.created_by_id IS NOT NULL))
	   AND LENGTH(m.text) >= $1
	   AND m.visibility IN ('V', 'A')
	   AND m.msg_type != 'V'
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
	Text        string `json:"text"`
}

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
		for _, idx := range indexes {
			if idx.Index != nil {
				_, err := rt.ES.Client.Indices.Delete(*idx.Index).Do(t.Context())
				require.NoError(t, err)
			}
		}
	}

	return msgs
}

// SearchAssertion is a search query and the expected contact IDs that should match.
type SearchAssertion struct {
	Query    string             `json:"query"`
	Contacts []models.ContactID `json:"contacts"`
}

// removes all documents from the contacts index and deletes all message indexes.
// Callers should flush the ES writer first if there may be buffered writes.
func clearElasticIndexes(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	// clear contacts
	_, err := rt.ES.Client.DeleteByQuery(rt.Config.ElasticContactsIndex).
		Conflicts(conflicts.Proceed).
		Raw(strings.NewReader(`{"query": {"match_all": {}}}`)).Do(t.Context())
	require.NoError(t, err)

	_, err = rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(t.Context())
	require.NoError(t, err)

	// clear messages
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

// setupElasticContacts creates the contacts index in Elastic if it doesn't already exist
func setupElasticContacts(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	exists, err := rt.ES.Client.Indices.Exists(rt.Config.ElasticContactsIndex).IsSuccess(t.Context())
	require.NoError(t, err)

	if !exists {
		contactsBody := ReadFile(t, absPath("./testsuite/testdata/es_contacts.json"))
		_, err = rt.ES.Client.Indices.Create(rt.Config.ElasticContactsIndex).Raw(bytes.NewReader(contactsBody)).Do(t.Context())
		require.NoError(t, err)
	}
}

// setupElasticMessages creates the index template for messages in Elastic
func setupElasticMessages(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	messagesBody := ReadFile(t, absPath("./testsuite/testdata/es_messages.json"))

	// replace placeholder with actual index name for test
	body := bytes.ReplaceAll(messagesBody, []byte("{{INDEX}}"), []byte(rt.Config.ElasticMessagesIndex))

	_, err := rt.ES.Client.Indices.PutIndexTemplate(rt.Config.ElasticMessagesIndex).Raw(bytes.NewReader(body)).Do(t.Context())
	require.NoError(t, err)
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

		contacts, err := models.LoadContacts(ctx, rt.DB, oa, contactIDs)
		require.NoError(t, err)

		fcs := make([]*flows.Contact, 0, len(contacts))
		for _, mc := range contacts {
			fc, err := mc.EngineContact(oa)
			require.NoError(t, err)
			fcs = append(fcs, fc)
		}

		err = search.IndexContacts(ctx, rt, oa, fcs, map[models.ContactID]models.FlowID{})
		require.NoError(t, err)

		afterID = contactIDs[len(contactIDs)-1]
	}

	rt.ES.Writer.Flush()

	_, err = rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(ctx)
	require.NoError(t, err)
}
