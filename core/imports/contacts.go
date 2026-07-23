package imports

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/stringsx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/vinovest/sqlx"
)

// holds work data for import of a single contact
type importContact struct {
	record  int
	spec    *models.ContactSpec
	mc      *models.Contact
	created bool
	contact *core.Contact
	mods    []flows.Modifier
	errors  []string
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
	importsByContact := make(map[*core.Contact]*importContact, len(specs))
	for i := range imports {
		imports[i] = &importContact{record: b.RecordStart + i, spec: specs[i]}
	}

	if err := getOrCreateContacts(ctx, rt.DB, oa, userID, imports); err != nil {
		return fmt.Errorf("error getting and creating contacts: %w", err)
	}

	// gather up contacts and modifiers
	mcs := make([]*models.Contact, 0, len(imports))
	contacts := make([]*core.Contact, 0, len(imports))
	mods := make(map[models.ContactID][]flows.Modifier, len(imports))
	for _, imp := range imports {
		// ignore errored imports which couldn't get/create a contact
		if imp.mc != nil {
			mcs = append(mcs, imp.mc)
			contacts = append(contacts, imp.contact)
			mods[imp.mc.ID()] = imp.mods
			importsByContact[imp.contact] = imp
		}
	}

	// and apply in bulk
	eventsByContact, err := runner.ModifyWithoutLock(ctx, rt, oa, userID, mcs, contacts, mods, models.ViaImport)
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

		// URN validation limits path length but identities (scheme:path) also have to fit in the database, and
		// imports are one of the few places un-checked URNs can come from
		if urn, ok := findOversizedURN(spec.URNs); ok {
			addError("URN '%s' is too long", stringsx.TruncateEllipsis(string(urn.Identity()), 32))
			continue
		}

		isActive := spec.Status == "" || spec.Status == core.ContactStatusActive

		uuid := spec.UUID
		if uuid != "" {
			imp.mc = contactsByUUID[uuid]
			if imp.mc == nil {
				addError("Unable to find contact with UUID '%s'", uuid)
				continue
			}

			imp.contact, err = imp.mc.EngineContact(oa)
			if err != nil {
				return fmt.Errorf("error creating engine contact for %d: %w", imp.mc.ID(), err)
			}

		} else {
			imp.mc, imp.contact, imp.created, err = models.GetOrCreateContact(ctx, db, oa, userID, spec.URNs, models.NilChannelID)
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
			if spec.Status == core.ContactStatusArchived || spec.Status == core.ContactStatusBlocked || spec.Status == core.ContactStatusStopped {
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
			groups := make([]*core.Group, 0, len(spec.Groups))
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

// contacts_contacturn.identity is varchar(255) so URNs whose identities exceed that can't be saved
const maxURNIdentityLength = 255

// returns the first URN whose normalized identity is too long to be saved, if any
func findOversizedURN(urnz []urns.URN) (urns.URN, bool) {
	for _, urn := range urnz {
		urn := urn.Normalize()
		if utf8.RuneCountInString(string(urn.Identity())) > maxURNIdentityLength {
			return urn, true
		}
	}
	return urns.NilURN, false
}

// loads any import contacts for which we have UUIDs
func loadContactsByUUID(ctx context.Context, db *sqlx.DB, oa *models.OrgAssets, imports []*importContact) (map[core.ContactUUID]*models.Contact, error) {
	uuids := make([]core.ContactUUID, 0, 50)
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

	contactsByUUID := make(map[core.ContactUUID]*models.Contact, len(contacts))
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
		if imp.mc == nil {
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
