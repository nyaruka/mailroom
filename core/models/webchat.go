package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/cache"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// WebChatVisitor is the contact behind a chat on a webchat channel
type WebChatVisitor struct {
	OrgID       OrgID            `db:"org_id"`
	ContactUUID core.ContactUUID `db:"contact_uuid"`
}

type webChatKey struct {
	channelUUID assets.ChannelUUID
	chatID      string
}

// visitors are looked up on every typing pulse so are cached, but only once found - a chat that's only just been
// started may not be visible yet and shouldn't be shut out until a cached miss expires. A visitor that's since been
// deleted can outlive that by a TTL, which only lets typing reach agents for a little longer.
var webChatCache *cache.Local[webChatKey, *WebChatVisitor]

var errNoWebChatVisitor = errors.New("no such webchat visitor")

func initWebChatCache(rt *runtime.Runtime) {
	if webChatCache != nil {
		webChatCache.Stop()
	}

	webChatCache = cache.NewLocal(func(ctx context.Context, k webChatKey) (*WebChatVisitor, error) {
		v, err := loadWebChatVisitor(ctx, rt.DB, k.channelUUID, k.chatID)
		if err == nil && v == nil {
			return nil, errNoWebChatVisitor // errors aren't cached
		}
		return v, err
	}, time.Minute, 10_000) // bounded as keys come from clients, even if only valid ones are cached
	webChatCache.Start()
}

// GetWebChatVisitor returns the active contact for the given chat on the given active webchat channel, or nil if
// there's no such chat. The chat's URN must be attached to that channel - which is what the platform checks when
// visitors subscribe to the chat's socket.
func GetWebChatVisitor(ctx context.Context, channelUUID assets.ChannelUUID, chatID string) (*WebChatVisitor, error) {
	v, err := webChatCache.GetOrFetch(ctx, webChatKey{channelUUID, chatID})
	if errors.Is(err, errNoWebChatVisitor) {
		return nil, nil
	}
	return v, err
}

const sqlSelectWebChatVisitor = `
SELECT ch.org_id, c.uuid AS contact_uuid
  FROM channels_channel ch
  JOIN contacts_contacturn u ON u.channel_id = ch.id AND u.identity = 'webchat:' || $2
  JOIN contacts_contact c ON c.id = u.contact_id AND c.is_active
 WHERE ch.uuid = $1 AND ch.channel_type = 'WCH' AND ch.is_active`

func loadWebChatVisitor(ctx context.Context, db DBorTx, channelUUID assets.ChannelUUID, chatID string) (*WebChatVisitor, error) {
	v := &WebChatVisitor{}
	if err := db.GetContext(ctx, v, sqlSelectWebChatVisitor, channelUUID, chatID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("error loading webchat visitor: %w", err)
	}
	return v, nil
}
