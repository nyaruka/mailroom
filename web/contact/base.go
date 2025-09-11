package contact

import (
	"fmt"
	"maps"
	"slices"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/models"
)

// Creation a validated contact creation task
type Creation struct {
	Name     string
	Language i18n.Language
	Status   models.ContactStatus
	URNs     []urns.URN
	Mods     []flows.Modifier
}

// SpecToCreation validates that the spec is valid for the given assets
func SpecToCreation(s *models.ContactSpec, env envs.Environment, sa flows.SessionAssets) (*Creation, error) {
	var err error
	validated := &Creation{}

	if s.Name != nil {
		validated.Name = *s.Name
	}

	if s.Language != nil && *s.Language != "" {
		validated.Language, err = i18n.ParseLanguage(*s.Language)
		if err != nil {
			return nil, fmt.Errorf("invalid language: %w", err)
		}
	}

	if s.Status != "" {
		validated.Status = models.ContactToModelStatus[s.Status]
	} else {
		validated.Status = models.ContactStatusActive
	}

	validated.URNs = make([]urns.URN, len(s.URNs))
	for i, urn := range s.URNs {
		validated.URNs[i] = urn.Normalize()
	}

	validated.Mods = make([]flows.Modifier, 0, len(s.Fields))

	for _, key := range slices.Sorted(maps.Keys(s.Fields)) { // for test determinism
		field := sa.Fields().Get(key)
		if field == nil {
			return nil, fmt.Errorf("unknown contact field '%s'", key)
		}
		if s.Fields[key] != "" {
			validated.Mods = append(validated.Mods, modifiers.NewField(field, s.Fields[key]))
		}
	}

	if len(s.Groups) > 0 {
		groups := make([]*flows.Group, len(s.Groups))
		for i, uuid := range s.Groups {
			group := sa.Groups().Get(uuid)
			if group == nil {
				return nil, fmt.Errorf("unknown contact group '%s'", uuid)
			}
			if group.UsesQuery() {
				return nil, fmt.Errorf("can't add contact to query based group '%s'", uuid)
			}
			groups[i] = group
		}

		validated.Mods = append(validated.Mods, modifiers.NewGroups(groups, modifiers.GroupsAdd))
	}

	return validated, nil
}
