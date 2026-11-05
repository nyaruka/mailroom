package public

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"google.golang.org/protobuf/proto"
)

func init() {
	web.InternetRoute(http.MethodGet, "/org/{uuid:[0-9a-f\\-]+}/metrics", handleMetrics)
}

const groupCountsSQL = `
         SELECT g.id, g.name, g.uuid, g.is_system, COALESCE(SUM(c.count), 0) AS count 
           FROM contacts_contactgroup g 
LEFT OUTER JOIN contacts_contactgroupcount c ON c.group_id = g.id 
          WHERE g.org_id = $1 AND g.is_active = TRUE 
       GROUP BY g.id;`

type groupCountRow struct {
	ID       models.GroupID   `db:"id"`
	Name     string           `db:"name"`
	UUID     assets.GroupUUID `db:"uuid"`
	IsSystem bool             `db:"is_system"`
	Count    int64            `db:"count"`
}

func calculateGroupCounts(ctx context.Context, rt *runtime.Runtime, org *models.Org) (*dto.MetricFamily, error) {
	rows, err := rt.DB.QueryxContext(ctx, groupCountsSQL, org.ID())
	if err != nil {
		return nil, fmt.Errorf("error querying group counts for org: %w", err)
	}
	defer rows.Close()

	family := &dto.MetricFamily{
		Name:   proto.String("rapidpro_group_contact_count"),
		Help:   proto.String("the number of contacts in various groups"),
		Type:   dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{},
	}

	row := &groupCountRow{}
	for rows.Next() {
		err = rows.StructScan(row)
		if err != nil {
			return nil, fmt.Errorf("error scanning group count row: %w", err)
		}

		groupType := "user"
		if row.IsSystem {
			groupType = "system"
		}

		family.Metric = append(family.Metric,
			&dto.Metric{
				Label: []*dto.LabelPair{
					{
						Name:  proto.String("group_name"),
						Value: proto.String(strings.TrimPrefix(row.Name, "\\")),
					},
					{
						Name:  proto.String("group_uuid"),
						Value: proto.String(string(row.UUID)),
					},
					{
						Name:  proto.String("group_type"),
						Value: proto.String(groupType),
					},
					{
						Name:  proto.String("org"),
						Value: proto.String(org.Name()),
					},
				},
				Gauge: &dto.Gauge{
					Value: proto.Float64(float64(row.Count)),
				},
			},
		)
	}

	return family, err
}

const channelsSQL = `
SELECT uuid, name, role, channel_type
  FROM channels_channel
 WHERE org_id = $1 AND is_active = TRUE;`

type channelRow struct {
	UUID        assets.ChannelUUID `db:"uuid"`
	Name        string             `db:"name"`
	Role        string             `db:"role"`
	ChannelType string             `db:"channel_type"`
}

// calculateChannelCounts returns a zero count for every message direction and type each channel supports. Counts are
// no longer supported, but the metrics are kept so that existing scrapers and dashboards don't break.
func calculateChannelCounts(ctx context.Context, rt *runtime.Runtime, org *models.Org) (*dto.MetricFamily, error) {
	rows, err := rt.DB.QueryxContext(ctx, channelsSQL, org.ID())
	if err != nil {
		return nil, fmt.Errorf("error querying channels for org: %w", err)
	}
	defer rows.Close()

	channels := make([]*channelRow, 0)
	for rows.Next() {
		channel := &channelRow{}
		if err := rows.StructScan(channel); err != nil {
			return nil, fmt.Errorf("error scanning channel row: %w", err)
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading channel rows: %w", err)
	}

	family := &dto.MetricFamily{
		Name:   proto.String("rapidpro_channel_msg_count"),
		Help:   proto.String("no longer supported and always 0"),
		Type:   dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{},
	}

	for _, channel := range channels {
		for _, role := range channel.Role {
			direction, msgType, ok := channelRoleMetrics(role)
			if !ok {
				continue
			}

			family.Metric = append(family.Metric,
				&dto.Metric{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("channel_name"),
							Value: proto.String(channel.Name),
						},
						{
							Name:  proto.String("channel_uuid"),
							Value: proto.String(string(channel.UUID)),
						},
						{
							Name:  proto.String("channel_type"),
							Value: proto.String(channel.ChannelType),
						},
						{
							Name:  proto.String("msg_direction"),
							Value: proto.String(direction),
						},
						{
							Name:  proto.String("msg_type"),
							Value: proto.String(msgType),
						},
						{
							Name:  proto.String("org"),
							Value: proto.String(org.Name()),
						},
					},
					Gauge: &dto.Gauge{
						Value: proto.Float64(0),
					},
				},
			)
		}
	}

	return family, nil
}

// channelRoleMetrics returns the message direction and type of the count for the given channel role, if it has one
func channelRoleMetrics(role rune) (string, string, bool) {
	switch role {
	case 'S':
		return "out", "message", true
	case 'R':
		return "in", "message", true
	case 'C':
		return "out", "voice", true
	case 'A':
		return "in", "voice", true
	}
	return "", "", false
}

func handleMetrics(ctx context.Context, rt *runtime.Runtime, r *http.Request, rawW http.ResponseWriter) error {
	// we should have basic auth headers, username should be metrics
	username, token, ok := r.BasicAuth()
	if !ok || username != "metrics" {
		rawW.WriteHeader(http.StatusUnauthorized)
		rawW.Write([]byte(`{"error": "invalid authentication"}`))
		return nil
	}

	orgID, err := models.GetOrgIDFromUUID(ctx, rt.DB.DB, models.OrgUUID(r.PathValue("uuid")))
	if err != nil {
		return fmt.Errorf("error looking up org by UUID: %w", err)
	}

	if orgID == models.NilOrgID {
		rawW.WriteHeader(http.StatusUnauthorized)
		rawW.Write([]byte(`{"error": "invalid authentication"}`))
		return nil
	}

	oa, err := models.GetOrgAssets(ctx, rt, orgID)
	if err != nil {
		return fmt.Errorf("error loading org assets: %w", err)
	}

	if oa.Org().PrometheusToken() != token {
		rawW.WriteHeader(http.StatusUnauthorized)
		rawW.Write([]byte(`{"error": "invalid authentication"}`))
		return nil
	}

	groups, err := calculateGroupCounts(ctx, rt, oa.Org())
	if err != nil {
		return fmt.Errorf("error calculating group counts for org: %d: %w", oa.OrgID(), err)
	}

	channels, err := calculateChannelCounts(ctx, rt, oa.Org())
	if err != nil {
		return fmt.Errorf("error calculating channel counts for org: %d: %w", oa.OrgID(), err)
	}

	rawW.WriteHeader(http.StatusOK)

	_, err = expfmt.MetricFamilyToText(rawW, groups)
	if err != nil {
		return err
	}

	if len(channels.Metric) > 0 {
		_, err = expfmt.MetricFamilyToText(rawW, channels)
		if err != nil {
			return err
		}
	}

	return err
}
