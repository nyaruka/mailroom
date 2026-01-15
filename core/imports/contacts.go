package imports

import (
	"context"
	"fmt"
	"strings"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// holds work data for import of a single contact
type importContact struct {
	record      int
	spec        *models.ContactSpec
	contact     *models.Contact
	created     bool
	flowContact *flows.Contact
	mods        []flows.Modifier
	errors      []string
}

func ImportBatch(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, b *models.ContactImportBatch, userID models.UserID) error {
	if err := b.SetProcessing(ctx, rt.DB); err != nil {
		return fmt.Errorf("error marking as processing: %w", err)
	}

	// unmarshal this batch's specs
	var specs []*models.ContactSpec
	if err := jsonx.Unmarshal(b.Specs, &specs); err != nil {
		return fmt.Errorf("error unmarsaling specs: %w", err)
	}

	// create our work data for each contact being created or updated
	imports := make([]*importContact, len(specs))
	importsByContact := make(map[*flows.Contact]*importContact, len(specs))
	for i := range imports {
		imports[i] = &importContact{record: b.RecordStart + i, spec: specs[i]}
	}

	if err := getOrCreateContacts(ctx, rt.DB, oa, userID, imports); err != nil {
		return fmt.Errorf("error getting and creating contacts: %w", err)
	}

	// gather up contacts and modifiers
	mcs := make([]*models.Contact, 0, len(imports))
	contacts := make([]*flows.Contact, 0, len(imports))
	mods := make(map[flows.ContactUUID][]flows.Modifier, len(imports))
	for _, imp := range imports {
		// ignore errored imports which couldn't get/create a contact
		if imp.contact != nil {
			mcs = append(mcs, imp.contact)
			contacts = append(contacts, imp.flowContact)
			mods[imp.flowContact.UUID()] = imp.mods
			importsByContact[imp.flowContact] = imp
		}
	}

	// and apply in bulk
	eventsByContact, err := runner.BulkModify(ctx, rt, oa, userID, mcs, contacts, mods)
	if err != nil {
		return fmt.Errorf("error applying modifiers: %w", err)
	}

	// extract certain errors from events
	for contact, evts := range eventsByContact {
		for _, evt := range evts {
			switch typed := evt.(type) {
			case *events.Error:
				if typed.Code == events.ErrorCodeURNTaken {
					imp := importsByContact[contact]
					imp.errors = append(imp.errors, fmt.Sprintf("URN %s already taken by another contact", typed.Extra["urn"]))
				}
			}
		}
	}

	if err := markBatchComplete(ctx, rt.DB, b, imports); err != nil {
		return fmt.Errorf("unable to mark as complete: %w", err)
	}

	return nil
}

// for each import, fetches or creates the contact, creates the modifiers needed to set fields etc
func getOrCreateContacts(ctx context.Context, db *sqlx.DB, oa *models.OrgAssets, userID models.UserID, imports []*importContact) error {
	sa := oa.SessionAssets()

	// build map of UUIDs to contacts
	contactsByUUID, err := loadContactsByUUID(ctx, db, oa, imports)
	if err != nil {
		return fmt.Errorf("error loading contacts by UUID: %w", err)
	}

	for _, imp := range imports {
		addModifier := func(m flows.Modifier) { imp.mods = append(imp.mods, m) }
		addError := func(s string, args ...any) { imp.errors = append(imp.errors, fmt.Sprintf(s, args...)) }
		spec := imp.spec

		isActive := spec.Status == "" || spec.Status == flows.ContactStatusActive

		uuid := spec.UUID
		if uuid != "" {
			imp.contact = contactsByUUID[uuid]
			if imp.contact == nil {
				addError("Unable to find contact with UUID '%s'", uuid)
				continue
			}

			imp.flowContact, err = imp.contact.EngineContact(oa)
			if err != nil {
				return fmt.Errorf("error creating flow contact for %d: %w", imp.contact.ID(), err)
			}

		} else {
			imp.contact, imp.flowContact, imp.created, err = models.GetOrCreateContact(ctx, db, oa, userID, spec.URNs, models.NilChannelID)
			if err != nil {
				urnStrs := make([]string, len(spec.URNs))
				for i := range spec.URNs {
					urnStrs[i] = string(spec.URNs[i].Identity())
				}

				addError("Unable to find or create contact with URNs %s", strings.Join(urnStrs, ", "))
				continue
			}
		}

		addModifier(modifiers.NewURNs(spec.URNs, modifiers.URNsAppend))

		if spec.Name != nil {
			addModifier(modifiers.NewName(*spec.Name))
		}
		if spec.Language != nil {
			lang, err := i18n.ParseLanguage(*spec.Language)
			if err != nil {
				addError("'%s' is not a valid language code", *spec.Language)
			} else {
				addModifier(modifiers.NewLanguage(lang))
			}
		}
		if !isActive {
			if spec.Status == flows.ContactStatusArchived || spec.Status == flows.ContactStatusBlocked || spec.Status == flows.ContactStatusStopped {
				addModifier(modifiers.NewStatus(spec.Status))
			} else {
				addError("'%s' is not a valid status", spec.Status)
			}
		}

		for key, value := range spec.Fields {
			field := sa.Fields().Get(key)
			if field == nil {
				addError("'%s' is not a valid contact field key", key)
			} else {
				addModifier(modifiers.NewField(field, value))
			}
		}

		if len(spec.Groups) > 0 && isActive {
			groups := make([]*flows.Group, 0, len(spec.Groups))
			for _, uuid := range spec.Groups {
				group := sa.Groups().Get(uuid)
				if group == nil {
					addError("'%s' is not a valid contact group UUID", uuid)
				} else {
					groups = append(groups, group)
				}
			}
			addModifier(modifiers.NewGroups(groups, modifiers.GroupsAdd))
		}
	}

	return nil
}

// loads any import contacts for which we have UUIDs
func loadContactsByUUID(ctx context.Context, db *sqlx.DB, oa *models.OrgAssets, imports []*importContact) (map[flows.ContactUUID]*models.Contact, error) {
	uuids := make([]flows.ContactUUID, 0, 50)
	for _, imp := range imports {
		if imp.spec.UUID != "" {
			uuids = append(uuids, imp.spec.UUID)
		}
	}

	// build map of UUIDs to contacts
	contacts, err := models.LoadContactsByUUID(ctx, db, oa, uuids)
	if err != nil {
		return nil, err
	}

	contactsByUUID := make(map[flows.ContactUUID]*models.Contact, len(contacts))
	for _, c := range contacts {
		contactsByUUID[c.UUID()] = c
	}
	return contactsByUUID, nil
}

func markBatchComplete(ctx context.Context, db models.DBorTx, b *models.ContactImportBatch, imports []*importContact) error {
	numCreated := 0
	numUpdated := 0
	numErrored := 0
	importErrors := make([]models.ImportError, 0, 10)
	for _, imp := range imports {
		if imp.contact == nil {
			numErrored++
		} else if imp.created {
			numCreated++
		} else {
			numUpdated++
		}
		for _, e := range imp.errors {
			importErrors = append(importErrors, models.ImportError{Record: imp.record, Row: imp.spec.ImportRow, Message: e})
		}
	}

	return b.SetComplete(ctx, db, numCreated, numUpdated, numErrored, importErrors)
}
