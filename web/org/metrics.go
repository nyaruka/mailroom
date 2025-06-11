package org

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
	"github.com/nyaruka/null/v3"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"google.golang.org/protobuf/proto"
)

func init() {
	web.RegisterRoute(http.MethodGet, "/mr/org/{uuid:[0-9a-f\\-]+}/metrics", handleMetrics)
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

const channelCountsSQL = `
         SELECT ch.id, ch.uuid, ch.name, ch.role, ch.channel_type, c.scope, COALESCE(SUM(c.count), 0) as count 
           FROM channels_channel ch 
LEFT OUTER JOIN channels_channelcount c ON c.channel_id = ch.id 
          WHERE ch.org_id = $1 AND ch.is_active = TRUE
       GROUP BY ch.id, c.scope;`

type channelCountRow struct {
	ID          models.ChannelID   `db:"id"`
	UUID        assets.ChannelUUID `db:"uuid"`
	Name        string             `db:"name"`
	Role        string             `db:"role"`
	ChannelType string             `db:"channel_type"`
	Scope       null.String        `db:"scope"`
	Count       int64              `db:"count"`
}

type channelStats struct {
	ID          models.ChannelID
	UUID        assets.ChannelUUID
	Name        string
	Role        string
	ChannelType string
	Counts      map[string]int64
}

var channelCountScopeToType = map[string]string{
	"text:in":   "IM",
	"text:out":  "OM",
	"voice:in":  "IV",
	"voice:out": "OV",
}

func calculateChannelCounts(ctx context.Context, rt *runtime.Runtime, org *models.Org) (*dto.MetricFamily, error) {
	rows, err := rt.DB.QueryxContext(ctx, channelCountsSQL, org.ID())
	if err != nil {
		return nil, fmt.Errorf("error querying channel counts for org: %w", err)
	}
	defer rows.Close()

	// we build an intermediate struct here of possible values because we always want to expose all
	// possible metrics for a channel even if they aren't set. (IE, outgoing messages even if no messages
	// have been sent yet) So we build a channel dictionarly that initializes possible values based on the
	// role of the channel
	channels := make(map[assets.ChannelUUID]*channelStats)
	row := &channelCountRow{}
	for rows.Next() {
		err = rows.StructScan(row)
		if err != nil {
			return nil, fmt.Errorf("error scanning channel count row: %w", err)
		}

		channel, found := channels[row.UUID]
		if !found {
			channel = &channelStats{
				ID:          row.ID,
				UUID:        row.UUID,
				Name:        row.Name,
				Role:        row.Role,
				ChannelType: row.ChannelType,
				Counts:      make(map[string]int64),
			}
			channels[row.UUID] = channel

			// populate expected stats
			for _, role := range row.Role {
				switch role {
				case 'S':
					channel.Counts["OM"] = 0
				case 'R':
					channel.Counts["IM"] = 0
				case 'C':
					channel.Counts["OV"] = 0
				case 'A':
					channel.Counts["IV"] = 0
				}
			}
		}

		if row.Scope != "" {
			channel.Counts[channelCountScopeToType[string(row.Scope)]] = row.Count
		}
	}

	// now convert our normalized channels into our family of metrics
	family := &dto.MetricFamily{
		Name:   proto.String("rapidpro_channel_msg_count"),
		Help:   proto.String("the number of messages sent and received for a channel"),
		Type:   dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{},
	}

	for _, channel := range channels {
		for countType, count := range channel.Counts {
			// ignore channel log counts
			if countType[0] == 'L' {
				continue
			}

			direction := "in"
			if countType[0] == 'O' {
				direction = "out"
			}

			countType := "message"
			if countType[1] == 'V' {
				countType = "voice"
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
							Value: proto.String(countType),
						},
						{
							Name:  proto.String("org"),
							Value: proto.String(org.Name()),
						},
					},
					Gauge: &dto.Gauge{
						Value: proto.Float64(float64(count)),
					},
				},
			)
		}
	}

	return family, err
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
		return fmt.Errorf("unable to load org assets: %w", err)
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
