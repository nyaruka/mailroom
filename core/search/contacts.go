package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/operationtype"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/shopspring/decimal"
)

// ContactDocField represents a single field value in a contact document.
type ContactDocField struct {
	Field           assets.FieldUUID `json:"field"`
	Text            string           `json:"text,omitempty"`
	Number          *decimal.Decimal `json:"number,omitempty"`
	Datetime        *time.Time       `json:"datetime,omitempty"`
	State           string           `json:"state,omitempty"`
	StateKeyword    string           `json:"state_keyword,omitempty"`
	District        string           `json:"district,omitempty"`
	DistrictKeyword string           `json:"district_keyword,omitempty"`
	Ward            string           `json:"ward,omitempty"`
	WardKeyword     string           `json:"ward_keyword,omitempty"`
}

// ContactDocURN represents a single URN in a contact document.
type ContactDocURN struct {
	Scheme string `json:"scheme"`
	Path   string `json:"path"`
}

// ContactDoc represents a contact document in the contacts index. UUID is used as the document _id.
type ContactDoc struct {
	DBID           models.ContactID     `json:"id"`
	UUID           flows.ContactUUID    `json:"-"` // used as _id, not in body
	OrgID          models.OrgID         `json:"org_id"`
	Name           string               `json:"name,omitempty"`
	Status         models.ContactStatus `json:"status"`
	Language       i18n.Language        `json:"language,omitempty"`
	Fields         []*ContactDocField   `json:"fields,omitempty"`
	URNs           []*ContactDocURN     `json:"urns,omitempty"`
	GroupIDs       []models.GroupID     `json:"group_ids,omitempty"`
	FlowID         models.FlowID        `json:"flow_id,omitempty"`
	FlowHistoryIDs []models.FlowID      `json:"flow_history_ids,omitempty"`
	Tickets        int                  `json:"tickets"`
	CreatedOn      time.Time            `json:"created_on"`
	LastSeenOn     *time.Time           `json:"last_seen_on,omitempty"`
}

// NewContactDoc builds a ContactDoc from a flow contact and its org assets. We use the flow contact
// rather than the DB contact because it is kept up-to-date in memory as events are applied.
func NewContactDoc(oa *models.OrgAssets, c *flows.Contact, currentFlowID models.FlowID, flowHistoryIDs []models.FlowID) *ContactDoc {
	doc := &ContactDoc{
		UUID:           c.UUID(),
		DBID:           models.ContactID(c.ID()),
		OrgID:          oa.OrgID(),
		Name:           c.Name(),
		Status:         models.ContactToModelStatus[c.Status()],
		Language:       c.Language(),
		CreatedOn:      c.CreatedOn(),
		LastSeenOn:     c.LastSeenOn(),
		Tickets:        c.Tickets().Open().Count(),
		FlowID:         currentFlowID,
		FlowHistoryIDs: flowHistoryIDs,
	}

	// build field docs from the flow contact's field values
	for key, fv := range c.Fields() {
		if fv == nil {
			continue
		}

		value := fv.Value
		if value == nil {
			continue
		}

		field := oa.FieldByKey(key)
		if field == nil {
			continue
		}

		fd := &ContactDocField{Field: field.UUID()}

		if value.Text != nil && !value.Text.Empty() {
			fd.Text = value.Text.Native()
		}
		if value.Number != nil {
			n := value.Number.Native()
			fd.Number = &n
		}
		if value.Datetime != nil {
			t := value.Datetime.Native()
			fd.Datetime = &t
		}
		if value.State != "" {
			fd.State = string(value.State)
			fd.StateKeyword = value.State.Name()
		}
		if value.District != "" {
			fd.District = string(value.District)
			fd.DistrictKeyword = value.District.Name()
		}
		if value.Ward != "" {
			fd.Ward = string(value.Ward)
			fd.WardKeyword = value.Ward.Name()
		}

		doc.Fields = append(doc.Fields, fd)
	}

	// build URN docs
	for _, urn := range c.URNs() {
		doc.URNs = append(doc.URNs, &ContactDocURN{Scheme: urn.Scheme, Path: urn.Path})
	}

	// build group IDs by looking up the flow group UUIDs in the org assets
	for _, group := range c.Groups().All() {
		dbGroup := oa.GroupByUUID(group.UUID())
		if dbGroup != nil {
			doc.GroupIDs = append(doc.GroupIDs, dbGroup.ID())
		}
	}

	return doc
}

// IndexContacts builds contact documents and queues them for indexing in Elastic.
func IndexContacts(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, flowContacts []*flows.Contact, currentFlows map[models.ContactID]models.FlowID) error {
	if len(flowContacts) == 0 {
		return nil
	}

	contactIDs := make([]models.ContactID, len(flowContacts))
	for i, c := range flowContacts {
		contactIDs[i] = models.ContactID(c.ID())
	}

	flowHistoryByContact, err := models.GetContactFlowHistory(ctx, rt.DB, contactIDs)
	if err != nil {
		return fmt.Errorf("error loading flow history IDs: %w", err)
	}

	for _, c := range flowContacts {
		contactID := models.ContactID(c.ID())
		doc := NewContactDoc(oa, c, currentFlows[contactID], flowHistoryByContact[contactID])

		body, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("error marshalling contact doc: %w", err)
		}

		rt.ES.Writer.Queue(&elastic.Document{
			Index:   rt.Config.ElasticContactsIndex,
			ID:      string(doc.UUID),
			Routing: doc.OrgID.String(),
			Version: time.Now().UnixNano(),
			Body:    body,
		})
	}

	return nil
}

// DeindexContactsByUUID de-indexes the contacts with the given UUIDs from Elastic
func DeindexContactsByUUID(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, contactUUIDs []flows.ContactUUID) (int, error) {
	ids := make([]string, len(contactUUIDs))
	for i, uuid := range contactUUIDs {
		ids[i] = string(uuid)
	}

	cmds := &bytes.Buffer{}
	for _, id := range ids {
		cmds.Write(jsonx.MustMarshal(map[string]any{"delete": map[string]any{"_id": id}}))
		cmds.WriteString("\n")
	}

	resp, err := rt.ES.Client.Bulk().Index(rt.Config.ElasticContactsIndex).Routing(orgID.String()).Raw(bytes.NewReader(cmds.Bytes())).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("error deindexing deleted contacts from elastic: %w", err)
	}

	deleted := 0
	for _, r := range resp.Items {
		if r[operationtype.Delete].Status == 200 {
			deleted++
		}
	}

	return deleted, nil
}

// DeindexContactsByOrg de-indexes all contacts in the given org from Elastic
func DeindexContactsByOrg(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, limit int) (int, error) {
	src := map[string]any{
		"query":    elastic.Term("org_id", orgID),
		"max_docs": limit,
	}

	resp, err := rt.ES.Client.DeleteByQuery(rt.Config.ElasticContactsIndex).Routing(orgID.String()).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("error deindexing contacts in org #%d from elastic: %w", orgID, err)
	}

	return int(*resp.Deleted), nil
}
