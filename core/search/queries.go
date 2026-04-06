package search

import (
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
)

// BuildRecipientsQuery builds a query from a set of inclusions/exclusions (i.e. a flow start or broadcast)
func BuildRecipientsQuery(oa *models.OrgAssets, flow *models.Flow, groups []*models.Group, contactUUIDs []flows.ContactUUID, userQuery string, excs models.Exclusions, excGroups []*models.Group) (string, error) {
	var parsedQuery *contactql.ContactQuery
	var err error

	if userQuery != "" {
		parsedQuery, err = contactql.ParseQuery(oa.Env(), userQuery, oa.SessionAssets())
		if err != nil {
			return "", fmt.Errorf("invalid user query: %w", err)
		}
	}

	return contactql.Stringify(buildRecipientsQuery(oa.Env(), flow, groups, contactUUIDs, parsedQuery, excs, excGroups)), nil
}

func buildRecipientsQuery(env envs.Environment, flow *models.Flow, groups []*models.Group, contactUUIDs []flows.ContactUUID, userQuery *contactql.ContactQuery, excs models.Exclusions, excGroups []*models.Group) contactql.QueryNode {
	inclusions := make([]contactql.QueryNode, 0, 10)

	for _, group := range groups {
		inclusions = append(inclusions, contactql.NewCondition(contactql.PropertyTypeAttribute, "group", contactql.OpEqual, group.Name()))
	}
	for _, contactUUID := range contactUUIDs {
		inclusions = append(inclusions, contactql.NewCondition(contactql.PropertyTypeAttribute, "uuid", contactql.OpEqual, string(contactUUID)))
	}
	if userQuery != nil {
		inclusions = append(inclusions, userQuery.Root())
	}

	exclusions := make([]contactql.QueryNode, 0, 10)
	if excs.NonActive {
		exclusions = append(exclusions, contactql.NewCondition(contactql.PropertyTypeAttribute, "status", contactql.OpEqual, "active"))
	}
	if excs.InAFlow {
		exclusions = append(exclusions, contactql.NewCondition(contactql.PropertyTypeAttribute, "flow", contactql.OpEqual, ""))
	}
	if excs.StartedPreviously && flow != nil {
		exclusions = append(exclusions, contactql.NewCondition(contactql.PropertyTypeAttribute, "history", contactql.OpNotEqual, flow.Name()))
	}
	if excs.NotSeenSinceDays > 0 {
		seenSince := dates.Now().Add(-time.Hour * time.Duration(24*excs.NotSeenSinceDays))
		exclusions = append(exclusions, contactql.NewCondition(contactql.PropertyTypeAttribute, "last_seen_on", contactql.OpGreaterThan, formatQueryDate(env, seenSince)))
	}
	for _, group := range excGroups {
		exclusions = append(exclusions, contactql.NewCondition(contactql.PropertyTypeAttribute, "group", contactql.OpNotEqual, group.Name()))
	}

	return contactql.NewBoolCombination(contactql.BoolOperatorAnd,
		contactql.NewBoolCombination(contactql.BoolOperatorOr, inclusions...),
		contactql.NewBoolCombination(contactql.BoolOperatorAnd, exclusions...),
	).Simplify()
}

// formats a date for use in a query
func formatQueryDate(env envs.Environment, t time.Time) string {
	d := dates.ExtractDate(t.In(env.Timezone()))
	s, _ := d.Format(string(env.DateFormat()), env.DefaultLocale())
	return s
}
