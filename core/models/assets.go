package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/engine"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/runtime"
	cache "github.com/patrickmn/go-cache"
)

// Refresh is our type for the pieces of org assets we want fresh (not cached)
type Refresh int

// refresh bit masks
const (
	RefreshNone        = Refresh(0)
	RefreshAll         = Refresh(^0)
	RefreshOrg         = Refresh(1 << 1)
	RefreshCampaigns   = Refresh(1 << 2)
	RefreshChannels    = Refresh(1 << 3)
	RefreshClassifiers = Refresh(1 << 4)
	RefreshFields      = Refresh(1 << 5)
	RefreshFlows       = Refresh(1 << 6)
	RefreshGlobals     = Refresh(1 << 7)
	RefreshGroups      = Refresh(1 << 8)
	RefreshLabels      = Refresh(1 << 9)
	RefreshLLMs        = Refresh(1 << 10)
	RefreshLocations   = Refresh(1 << 11)
	RefreshOptIns      = Refresh(1 << 12)
	RefreshResthooks   = Refresh(1 << 13)
	RefreshTemplates   = Refresh(1 << 14)
	RefreshTopics      = Refresh(1 << 15)
	RefreshTriggers    = Refresh(1 << 16)
	RefreshUsers       = Refresh(1 << 17)
)

// OrgAssets is our top level cache of all things contained in an org. It is used to build
// SessionAssets for the engine but also used to cache campaigns and other org level attributes
type OrgAssets struct {
	rt      *runtime.Runtime
	builtAt time.Time

	orgID OrgID

	org *Org

	sessionAssets flows.SessionAssets

	flowByUUID    map[assets.FlowUUID]assets.Flow
	flowByID      map[FlowID]assets.Flow
	flowCacheLock sync.RWMutex

	campaigns             []assets.Campaign
	campaignPointsByField map[FieldID][]*CampaignPoint
	campaignPointsByID    map[PointID]*CampaignPoint
	campaignsByGroup      map[GroupID][]*Campaign

	channels       []assets.Channel
	channelsByID   map[ChannelID]*Channel
	channelsByUUID map[assets.ChannelUUID]*Channel

	classifiers       []assets.Classifier
	classifiersByUUID map[assets.ClassifierUUID]*Classifier

	fields       []assets.Field // excludes proxy fields
	fieldsByUUID map[assets.FieldUUID]*Field
	fieldsByKey  map[string]*Field

	groups       []assets.Group
	groupsByID   map[GroupID]*Group
	groupsByUUID map[assets.GroupUUID]*Group

	labels       []assets.Label
	labelsByUUID map[assets.LabelUUID]*Label

	llms     []assets.LLM
	llmsByID map[LLMID]*LLM

	optIns       []assets.OptIn
	optInsByID   map[OptInID]*OptIn
	optInsByUUID map[assets.OptInUUID]*OptIn

	templates       []assets.Template
	templatesByID   map[TemplateID]*Template
	templatesByUUID map[assets.TemplateUUID]*Template

	topics       []assets.Topic
	topicsByID   map[TopicID]*Topic
	topicsByUUID map[assets.TopicUUID]*Topic

	resthooks []assets.Resthook
	triggers  []*Trigger
	globals   []assets.Global

	locations        []assets.LocationHierarchy
	locationsBuiltAt time.Time

	users       []assets.User
	usersByID   map[UserID]*User
	usersByUUID map[assets.UserUUID]*User
}

var ErrNotFound = errors.New("not found")

// we cache org objects for 5 seconds, cleanup every minute (gets never return expired items)
var orgCache = cache.New(time.Second*5, time.Minute)

// map of org id -> assetLoader used to make sure we only load an individual org once when expired
var assetLoaders = sync.Map{}

// represents a goroutine loading assets for an org, stores the loaded assets (and possible error) and
// a channel to notify any listeners that the loading is complete
type assetLoader struct {
	done   chan struct{}
	assets *OrgAssets
	err    error
}

// loadOrgAssetsOnce is a thread safe method to create new org assets from the DB in a thread safe manner
// that ensures only one goroutine is fetching the org at once. (others will block on the first completing)
func loadOrgAssetsOnce(ctx context.Context, rt *runtime.Runtime, orgID OrgID) (*OrgAssets, error) {
	loader := assetLoader{done: make(chan struct{})}
	actual, inFlight := assetLoaders.LoadOrStore(orgID, &loader)
	actualLoader := actual.(*assetLoader)
	if inFlight {
		<-actualLoader.done
	} else {
		actualLoader.assets, actualLoader.err = NewOrgAssets(ctx, rt, orgID, nil, RefreshAll)
		close(actualLoader.done)
		assetLoaders.Delete(orgID)
	}
	return actualLoader.assets, actualLoader.err
}

// FlushCache clears our entire org cache
func FlushCache() {
	orgCache.Flush()
}

// NewOrgAssets creates and returns a new org assets objects, potentially using the previous
// org assets passed in to prevent refetching locations
func NewOrgAssets(ctx context.Context, rt *runtime.Runtime, orgID OrgID, prev *OrgAssets, refresh Refresh) (*OrgAssets, error) {
	// It's generally safe to load assets from the read replica since in mailroom they are immutable - but sometimes we're
	// responding to asset changes in RP (e.g. populating a new group). For simplicity we assume any explicit refresh
	// request means that we need to use the primary DB.
	db := rt.DB.DB
	if refresh == RefreshNone {
		db = rt.ReadonlyDB
	}

	// build our new assets
	oa := &OrgAssets{
		rt:      rt,
		builtAt: time.Now(),
		orgID:   orgID,
	}

	// inherit our built at if we are reusing anything
	if prev != nil && refresh&RefreshAll > 0 {
		oa.builtAt = prev.builtAt
	}

	// we load everything at once except for flows which are lazily loaded
	var err error

	if prev == nil || refresh&RefreshOrg > 0 {
		oa.org, err = LoadOrg(ctx, db, orgID)
		if err != nil {
			return nil, fmt.Errorf("error loading environment for org %d: %w", orgID, err)
		}
	} else {
		oa.org = prev.org
	}

	if prev == nil || refresh&RefreshCampaigns > 0 {
		oa.campaigns, err = loadAssetType(ctx, db, orgID, "campaigns", loadCampaigns)
		if err != nil {
			return nil, fmt.Errorf("error loading campaigns for org %d: %w", orgID, err)
		}
		oa.campaignPointsByField = make(map[FieldID][]*CampaignPoint)
		oa.campaignPointsByID = make(map[PointID]*CampaignPoint)
		oa.campaignsByGroup = make(map[GroupID][]*Campaign)
		for _, c := range oa.campaigns {
			camp := c.(*Campaign)
			oa.campaignsByGroup[camp.GroupID()] = append(oa.campaignsByGroup[camp.GroupID()], camp)
			for _, e := range camp.Points() {
				oa.campaignPointsByField[e.RelativeToID] = append(oa.campaignPointsByField[e.RelativeToID], e)
				oa.campaignPointsByID[e.ID] = e
			}
		}
	} else {
		oa.campaigns = prev.campaigns
		oa.campaignPointsByField = prev.campaignPointsByField
		oa.campaignPointsByID = prev.campaignPointsByID
		oa.campaignsByGroup = prev.campaignsByGroup
	}

	if prev == nil || refresh&RefreshChannels > 0 {
		oa.channels, err = loadAssetType(ctx, db, orgID, "channels", loadChannels)
		if err != nil {
			return nil, fmt.Errorf("error loading channel assets for org %d: %w", orgID, err)
		}
		oa.channelsByID = make(map[ChannelID]*Channel)
		oa.channelsByUUID = make(map[assets.ChannelUUID]*Channel)
		for _, c := range oa.channels {
			channel := c.(*Channel)
			oa.channelsByID[channel.ID()] = channel
			oa.channelsByUUID[channel.UUID()] = channel
		}
	} else {
		oa.channels = prev.channels
		oa.channelsByID = prev.channelsByID
		oa.channelsByUUID = prev.channelsByUUID
	}

	if prev == nil || refresh&RefreshFields > 0 {
		fields, err := loadAssetType(ctx, db, orgID, "fields", loadFields)
		if err != nil {
			return nil, fmt.Errorf("error loading field assets for org %d: %w", orgID, err)
		}
		oa.fields = make([]assets.Field, 0, len(fields))
		oa.fieldsByUUID = make(map[assets.FieldUUID]*Field, len(fields))
		oa.fieldsByKey = make(map[string]*Field, len(fields))
		for _, f := range fields {
			field := f.(*Field)
			oa.fieldsByUUID[field.UUID()] = field
			oa.fieldsByKey[field.Key()] = field

			if !field.Proxy() {
				oa.fields = append(oa.fields, f)
			}
		}
	} else {
		oa.fields = prev.fields
		oa.fieldsByUUID = prev.fieldsByUUID
		oa.fieldsByKey = prev.fieldsByKey
	}

	if prev == nil || refresh&RefreshGroups > 0 {
		groups, err := loadAssetType(ctx, db, orgID, "groups", loadGroups)
		if err != nil {
			return nil, fmt.Errorf("error loading group assets for org %d: %w", orgID, err)
		}
		oa.groups = make([]assets.Group, 0, len(groups))
		oa.groupsByID = make(map[GroupID]*Group, len(groups))
		oa.groupsByUUID = make(map[assets.GroupUUID]*Group, len(groups))
		for _, g := range groups {
			group := g.(*Group)
			oa.groupsByID[group.ID()] = group
			oa.groupsByUUID[group.UUID()] = group

			if group.Visible() {
				oa.groups = append(oa.groups, g)
			}
		}
	} else {
		oa.groups = prev.groups
		oa.groupsByID = prev.groupsByID
		oa.groupsByUUID = prev.groupsByUUID
	}

	if prev == nil || refresh&RefreshClassifiers > 0 {
		oa.classifiers, err = loadAssetType(ctx, db, orgID, "classifiers", loadClassifiers)
		if err != nil {
			return nil, fmt.Errorf("error loading classifier assets for org %d: %w", orgID, err)
		}
		oa.classifiersByUUID = make(map[assets.ClassifierUUID]*Classifier)
		for _, c := range oa.classifiers {
			oa.classifiersByUUID[c.UUID()] = c.(*Classifier)
		}
	} else {
		oa.classifiers = prev.classifiers
		oa.classifiersByUUID = prev.classifiersByUUID
	}

	if prev == nil || refresh&RefreshLabels > 0 {
		oa.labels, err = loadAssetType(ctx, db, orgID, "labels", loadLabels)
		if err != nil {
			return nil, fmt.Errorf("error loading labels for org %d: %w", orgID, err)
		}
		oa.labelsByUUID = make(map[assets.LabelUUID]*Label)
		for _, l := range oa.labels {
			oa.labelsByUUID[l.UUID()] = l.(*Label)
		}
	} else {
		oa.labels = prev.labels
		oa.labelsByUUID = prev.labelsByUUID
	}

	if prev == nil || refresh&RefreshLLMs > 0 {
		oa.llms, err = loadAssetType(ctx, db, orgID, "llms", loadLLMs)
		if err != nil {
			return nil, fmt.Errorf("error loading LLMs for org %d: %w", orgID, err)
		}
		oa.llmsByID = make(map[LLMID]*LLM)
		for _, l := range oa.llms {
			oa.llmsByID[l.(*LLM).ID()] = l.(*LLM)
		}
	} else {
		oa.llms = prev.llms
		oa.llmsByID = prev.llmsByID
	}

	if prev == nil || refresh&RefreshOptIns > 0 {
		oa.optIns, err = loadAssetType(ctx, db, orgID, "optins", loadOptIns)
		if err != nil {
			return nil, fmt.Errorf("error loading optins for org %d: %w", orgID, err)
		}
		oa.optInsByID = make(map[OptInID]*OptIn)
		oa.optInsByUUID = make(map[assets.OptInUUID]*OptIn)
		for _, o := range oa.optIns {
			optIn := o.(*OptIn)
			oa.optInsByID[optIn.ID()] = optIn
			oa.optInsByUUID[optIn.UUID()] = optIn
		}
	} else {
		oa.optIns = prev.optIns
		oa.optInsByUUID = prev.optInsByUUID
	}

	if prev == nil || refresh&RefreshResthooks > 0 {
		oa.resthooks, err = loadAssetType(ctx, db, orgID, "resthooks", loadResthooks)
		if err != nil {
			return nil, fmt.Errorf("error loading resthooks for org %d: %w", orgID, err)
		}
	} else {
		oa.resthooks = prev.resthooks
	}

	if prev == nil || refresh&RefreshTriggers > 0 {
		oa.triggers, err = loadAssetType(ctx, db, orgID, "triggers", loadTriggers)
		if err != nil {
			return nil, fmt.Errorf("error loading triggers for org %d: %w", orgID, err)
		}
	} else {
		oa.triggers = prev.triggers
	}

	if prev == nil || refresh&RefreshTemplates > 0 {
		oa.templates, err = loadAssetType(ctx, db, orgID, "templates", loadTemplates)
		if err != nil {
			return nil, fmt.Errorf("error loading templates for org %d: %w", orgID, err)
		}
		oa.templatesByID = make(map[TemplateID]*Template)
		oa.templatesByUUID = make(map[assets.TemplateUUID]*Template)
		for _, t := range oa.templates {
			oa.templatesByID[t.(*Template).ID()] = t.(*Template)
			oa.templatesByUUID[t.UUID()] = t.(*Template)
		}
	} else {
		oa.templates = prev.templates
		oa.templatesByID = prev.templatesByID
		oa.templatesByUUID = prev.templatesByUUID
	}

	if prev == nil || refresh&RefreshGlobals > 0 {
		oa.globals, err = loadAssetType(ctx, db, orgID, "globals", loadGlobals)
		if err != nil {
			return nil, fmt.Errorf("error loading globals for org %d: %w", orgID, err)
		}
	} else {
		oa.globals = prev.globals
	}

	if prev == nil || refresh&RefreshLocations > 0 {
		oa.locations, err = loadLocations(ctx, db, oa)
		oa.locationsBuiltAt = time.Now()
		if err != nil {
			return nil, fmt.Errorf("error loading group locations for org %d: %w", orgID, err)
		}
	} else {
		oa.locations = prev.locations
		oa.locationsBuiltAt = prev.locationsBuiltAt
	}

	if prev == nil || refresh&RefreshFlows > 0 {
		oa.flowByUUID = make(map[assets.FlowUUID]assets.Flow)
		oa.flowByID = make(map[FlowID]assets.Flow)
	} else {
		oa.flowByUUID = prev.flowByUUID
		oa.flowByID = prev.flowByID
	}

	if prev == nil || refresh&RefreshTopics > 0 {
		oa.topics, err = loadAssetType(ctx, db, orgID, "topics", loadTopics)
		if err != nil {
			return nil, fmt.Errorf("error loading topic assets for org %d: %w", orgID, err)
		}
		oa.topicsByID = make(map[TopicID]*Topic, len(oa.topics))
		oa.topicsByUUID = make(map[assets.TopicUUID]*Topic, len(oa.topics))
		for _, t := range oa.topics {
			oa.topicsByID[t.(*Topic).ID()] = t.(*Topic)
			oa.topicsByUUID[t.UUID()] = t.(*Topic)
		}
	} else {
		oa.topics = prev.topics
		oa.topicsByID = prev.topicsByID
		oa.topicsByUUID = prev.topicsByUUID
	}

	if prev == nil || refresh&RefreshUsers > 0 {
		oa.users, err = loadAssetType(ctx, db, orgID, "users", loadUsers)
		if err != nil {
			return nil, fmt.Errorf("error loading user assets for org %d: %w", orgID, err)
		}
		oa.usersByID = make(map[UserID]*User)
		oa.usersByUUID = make(map[assets.UserUUID]*User)
		for _, u := range oa.users {
			oa.usersByID[u.(*User).ID()] = u.(*User)
			oa.usersByUUID[u.UUID()] = u.(*User)
		}
	} else {
		oa.users = prev.users
		oa.usersByID = prev.usersByID
		oa.usersByUUID = prev.usersByUUID
	}

	// intialize our session assets
	oa.sessionAssets, err = engine.NewSessionAssets(oa.Env(), oa, goflow.MigrationConfig(rt.Config))
	if err != nil {
		return nil, fmt.Errorf("error build session assets for org: %d: %w", orgID, err)
	}

	return oa, nil
}

// GetOrgAssets creates or gets org assets for the passed in org
func GetOrgAssets(ctx context.Context, rt *runtime.Runtime, orgID OrgID) (*OrgAssets, error) {
	return GetOrgAssetsWithRefresh(ctx, rt, orgID, RefreshNone)
}

// GetOrgAssetsWithRefresh creates or gets org assets for the passed in org refreshing the passed in assets
func GetOrgAssetsWithRefresh(ctx context.Context, rt *runtime.Runtime, orgID OrgID, refresh Refresh) (*OrgAssets, error) {
	// do we have a recent cache?
	key := fmt.Sprintf("%d", orgID)
	var cached *OrgAssets
	c, found := orgCache.Get(key)
	if found {
		cached = c.(*OrgAssets)
	}

	// if found and nothing to refresh, return it
	if found && refresh == RefreshNone {
		return cached, nil
	}

	// if it wasn't found at all, reload it
	if !found {
		o, err := loadOrgAssetsOnce(ctx, rt, orgID)
		if err != nil {
			return nil, err
		}

		// cache it for the future
		orgCache.SetDefault(key, o)
		return o, nil
	}

	// otherwise we need to refresh only some parts, go do that
	o, err := NewOrgAssets(ctx, rt, orgID, cached, refresh)
	if err != nil {
		return nil, err
	}

	orgCache.SetDefault(key, o)

	// return our assets
	return o, nil
}

func (a *OrgAssets) OrgID() OrgID { return a.orgID }

func (a *OrgAssets) Env() envs.Environment { return a.org.env }

func (a *OrgAssets) Org() *Org { return a.org }

func (a *OrgAssets) SessionAssets() flows.SessionAssets { return a.sessionAssets }

func (a *OrgAssets) Campaigns() ([]assets.Campaign, error) {
	return a.campaigns, nil
}

func (a *OrgAssets) CampaignByGroupID(groupID GroupID) []*Campaign {
	return a.campaignsByGroup[groupID]
}

func (a *OrgAssets) CampaignPointsByFieldID(fieldID FieldID) []*CampaignPoint {
	return a.campaignPointsByField[fieldID]
}

func (a *OrgAssets) CampaignPointByID(id PointID) *CampaignPoint {
	return a.campaignPointsByID[id]
}

func (a *OrgAssets) Channels() ([]assets.Channel, error) {
	return a.channels, nil
}

func (a *OrgAssets) ChannelByUUID(channelUUID assets.ChannelUUID) *Channel {
	return a.channelsByUUID[channelUUID]
}

func (a *OrgAssets) ChannelByID(channelID ChannelID) *Channel {
	return a.channelsByID[channelID]
}

func (a *OrgAssets) Classifiers() ([]assets.Classifier, error) {
	return a.classifiers, nil
}

func (a *OrgAssets) ClassifierByUUID(classifierUUID assets.ClassifierUUID) *Classifier {
	return a.classifiersByUUID[classifierUUID]
}

func (a *OrgAssets) Fields() ([]assets.Field, error) {
	return a.fields, nil
}

func (a *OrgAssets) FieldByUUID(fieldUUID assets.FieldUUID) *Field {
	return a.fieldsByUUID[fieldUUID]
}

func (a *OrgAssets) FieldByKey(key string) *Field {
	return a.fieldsByKey[key]
}

// CloneForSimulation clones our org assets for simulation and returns a new org assets with the given flow definitions overrided
func (a *OrgAssets) CloneForSimulation(ctx context.Context, rt *runtime.Runtime, newDefs map[assets.FlowUUID]json.RawMessage, testChannels []assets.Channel) (*OrgAssets, error) {
	// only channels and flows can be modified so only refresh those
	clone, err := NewOrgAssets(context.Background(), a.rt, a.OrgID(), a, RefreshFlows)
	if err != nil {
		return nil, err
	}

	for flowUUID, newDef := range newDefs {
		// get the original flow
		flowAsset, err := a.FlowByUUID(flowUUID)
		if err != nil {
			return nil, fmt.Errorf("unable to find flow with UUID '%s': %w", flowUUID, err)
		}
		f := flowAsset.(*Flow)

		// make a clone of the flow with the provided definition
		cf := f.cloneWithNewDefinition(newDef)

		clone.flowCacheLock.Lock()
		clone.flowByUUID[flowUUID] = cf
		clone.flowByID[cf.ID()] = cf
		clone.flowCacheLock.Unlock()
	}

	clone.channels = append(clone.channels, testChannels...)

	// rebuild our session assets with our new items
	clone.sessionAssets, err = engine.NewSessionAssets(a.Env(), clone, goflow.MigrationConfig(rt.Config))
	if err != nil {
		return nil, fmt.Errorf("error build session assets for org: %d: %w", clone.OrgID(), err)
	}

	return clone, err
}

// FlowByUUID returns the flow with the passed in UUID
func (a *OrgAssets) FlowByUUID(flowUUID assets.FlowUUID) (assets.Flow, error) {
	return a.loadFlow(
		func() assets.Flow {
			return a.flowByUUID[flowUUID]
		},
		func(ctx context.Context) (*Flow, error) {
			return LoadFlowByUUID(ctx, a.rt.ReadonlyDB, a.orgID, flowUUID)
		},
	)
}

// FlowByName returns the flow with the passed in name
func (a *OrgAssets) FlowByName(name string) (assets.Flow, error) {
	return a.loadFlow(
		func() assets.Flow {
			for _, f := range a.flowByUUID {
				if strings.EqualFold(f.Name(), name) {
					return f
				}
			}
			return nil
		},
		func(ctx context.Context) (*Flow, error) {
			return LoadFlowByName(ctx, a.rt.ReadonlyDB, a.orgID, name)
		},
	)
}

// FlowByID returns the flow with the passed in ID (unlike FlowByUUID, FlowByName returns *Flow rather than assets.Flow)
func (a *OrgAssets) FlowByID(flowID FlowID) (*Flow, error) {
	asset, err := a.loadFlow(
		func() assets.Flow {
			return a.flowByID[flowID]
		},
		func(ctx context.Context) (*Flow, error) {
			return LoadFlowByID(ctx, a.rt.ReadonlyDB, a.orgID, flowID)
		},
	)
	if err != nil {
		return nil, err
	}
	return asset.(*Flow), nil
}

func (a *OrgAssets) loadFlow(fromCache func() assets.Flow, fromDB func(context.Context) (*Flow, error)) (assets.Flow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	a.flowCacheLock.RLock()
	flow := fromCache()
	a.flowCacheLock.RUnlock()

	if flow != nil {
		return flow, nil
	}

	dbFlow, err := fromDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("error loading flow from db: %w", err)
	}

	if dbFlow == nil {
		return nil, ErrNotFound
	}

	a.flowCacheLock.Lock()
	a.flowByID[dbFlow.ID()] = dbFlow
	a.flowByUUID[dbFlow.UUID()] = dbFlow
	a.flowCacheLock.Unlock()

	return dbFlow, nil
}

func (a *OrgAssets) Groups() ([]assets.Group, error) {
	return a.groups, nil
}

func (a *OrgAssets) GroupByID(groupID GroupID) *Group {
	return a.groupsByID[groupID]
}

func (a *OrgAssets) GroupByUUID(groupUUID assets.GroupUUID) *Group {
	return a.groupsByUUID[groupUUID]
}

func (a *OrgAssets) Labels() ([]assets.Label, error) {
	return a.labels, nil
}

func (a *OrgAssets) LabelByUUID(uuid assets.LabelUUID) *Label {
	return a.labelsByUUID[uuid]
}

func (a *OrgAssets) LLMs() ([]assets.LLM, error) {
	return a.llms, nil
}

func (a *OrgAssets) LLMByID(id LLMID) *LLM {
	return a.llmsByID[id]
}

func (a *OrgAssets) Triggers() []*Trigger {
	return a.triggers
}

func (a *OrgAssets) Locations() ([]assets.LocationHierarchy, error) {
	return a.locations, nil
}

func (a *OrgAssets) OptIns() ([]assets.OptIn, error) {
	return a.optIns, nil
}

func (a *OrgAssets) OptInByID(id OptInID) *OptIn {
	return a.optInsByID[id]
}

func (a *OrgAssets) OptInByUUID(uuid assets.OptInUUID) *OptIn {
	return a.optInsByUUID[uuid]
}

func (a *OrgAssets) Resthooks() ([]assets.Resthook, error) {
	return a.resthooks, nil
}

func (a *OrgAssets) ResthookBySlug(slug string) *Resthook {
	for _, r := range a.resthooks {
		if r.Slug() == slug {
			return r.(*Resthook)
		}
	}
	return nil
}

func (a *OrgAssets) Templates() ([]assets.Template, error) {
	return a.templates, nil
}

func (a *OrgAssets) TemplateByID(id TemplateID) *Template {
	return a.templatesByID[id]
}

func (a *OrgAssets) TemplateByUUID(uuid assets.TemplateUUID) *Template {
	return a.templatesByUUID[uuid]
}

func (a *OrgAssets) Globals() ([]assets.Global, error) {
	return a.globals, nil
}

func (a *OrgAssets) Topics() ([]assets.Topic, error) {
	return a.topics, nil
}

func (a *OrgAssets) TopicByID(id TopicID) *Topic {
	return a.topicsByID[id]
}

func (a *OrgAssets) TopicByUUID(uuid assets.TopicUUID) *Topic {
	return a.topicsByUUID[uuid]
}

func (a *OrgAssets) Users() ([]assets.User, error) {
	return a.users, nil
}

func (a *OrgAssets) UserByID(id UserID) *User {
	return a.usersByID[id]
}

func (a *OrgAssets) UserByUUID(uuid assets.UserUUID) *User {
	return a.usersByUUID[uuid]
}

func loadAssetType[A any](ctx context.Context, db *sql.DB, orgID OrgID, name string, f func(ctx context.Context, db *sql.DB, orgID OrgID) ([]A, error)) ([]A, error) {
	start := time.Now()

	as, err := f(ctx, db, orgID)

	slog.Debug(fmt.Sprintf("loaded %s", name), "elapsed", time.Since(start), "org_id", orgID, "count", len(as))

	return as, err
}
