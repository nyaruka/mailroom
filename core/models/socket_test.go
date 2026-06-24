package models_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/centrifugal/gocent/v3"
	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryChannel(t *testing.T) {
	assert.Equal(t, "history:a393abc0-283d-4c9b-a1b3-641a035c34bf", models.HistoryChannel("a393abc0-283d-4c9b-a1b3-641a035c34bf"))
}

func TestSubscriptions(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	vc := rt.VK.Get()
	defer vc.Close()

	const ttl = 150 * time.Second
	contact1 := flows.ContactUUID("a393abc0-283d-4c9b-a1b3-641a035c34bf")
	contact2 := flows.ContactUUID("b699a406-7e44-49be-9f01-1a82893e8a10")
	hist1 := models.HistoryChannel(contact1)
	hist2 := models.HistoryChannel(contact2)

	assertSubscribed := func(channel string, expected bool) {
		t.Helper()
		actual, err := models.IsSubscribed(ctx, rt, channel)
		require.NoError(t, err)
		assert.Equal(t, expected, actual, "subscribed mismatch for %s", channel)
	}

	// nothing subscribed yet
	assertSubscribed(hist1, false)

	// recording a subscription marks the channel present with a TTL
	require.NoError(t, models.RecordSubscription(ctx, rt, hist1, ttl))
	assertSubscribed(hist1, true)

	secs, err := valkey.Int64(vc.Do("TTL", "socket-subs:"+hist1))
	require.NoError(t, err)
	assert.Greater(t, secs, int64(0))
	assert.LessOrEqual(t, secs, int64(ttl/time.Second))

	// a second subscriber to the same channel keeps it a single key (we track presence, not who)
	require.NoError(t, models.RecordSubscription(ctx, rt, hist1, ttl))
	assertvk.Keys(t, vc, "socket-subs:*", []string{"socket-subs:" + hist1})

	// a different channel is a separate key, checked independently
	assertSubscribed(hist2, false)
	require.NoError(t, models.RecordSubscription(ctx, rt, hist2, ttl))
	assertSubscribed(hist2, true)
	assertvk.Keys(t, vc, "socket-subs:*", []string{"socket-subs:" + hist1, "socket-subs:" + hist2})
}

func TestPublishToHistory(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	// a fake centrifugo API server that records what gets published to it
	type publish struct {
		Channel string          `json:"channel"`
		Data    json.RawMessage `json:"data"`
	}
	var mu sync.Mutex
	var published []publish

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the gocent client sends newline-delimited publish commands and expects one reply per command
		dec := json.NewDecoder(r.Body)
		replies := 0
		for {
			var cmd struct {
				Method string  `json:"method"`
				Params publish `json:"params"`
			}
			if err := dec.Decode(&cmd); err == io.EOF {
				break
			} else if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if cmd.Method == "publish" {
				mu.Lock()
				published = append(published, cmd.Params)
				mu.Unlock()
			}
			replies++
		}
		for range replies {
			io.WriteString(w, `{"result":{}}`+"\n")
		}
	}))
	defer srv.Close()

	snapshot := func() []publish {
		mu.Lock()
		defer mu.Unlock()
		return append([]publish(nil), published...)
	}

	contact := flows.ContactUUID("a393abc0-283d-4c9b-a1b3-641a035c34bf")
	channel := models.HistoryChannel(contact)
	evt1 := events.NewContactNameChanged("Bob")
	evt1.SetUser(assets.NewUserReference("eb9536d7-7b22-4ca6-9a1e-8e1f1effe7f3", "Ann Admin"), "ui")
	evt2 := events.NewContactLanguageChanged("spa")

	// no centrifugo client configured = no-op
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, []flows.Event{evt1}))
	assert.Empty(t, snapshot())

	rt.Centrifugo = gocent.New(gocent.Config{Addr: srv.URL, Key: "sesame"})

	// channel isn't subscribed yet, so still nothing is published
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, []flows.Event{evt1}))
	assert.Empty(t, snapshot())

	// empty event slice is a no-op even when subscribed
	require.NoError(t, models.RecordSubscription(ctx, rt, channel, 150*time.Second))
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, nil))
	assert.Empty(t, snapshot())

	// now that it's subscribed, each event is published to the contact's history channel as its full JSON
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, []flows.Event{evt1, evt2}))

	sent := snapshot()
	require.Len(t, sent, 2)
	assert.Equal(t, channel, sent[0].Channel)
	assert.Equal(t, channel, sent[1].Channel)

	// the published payload is the full event including its uuid (the history table strips uuid into the key)
	// and the raw _user reference (uuid+name) that the read path later hydrates with avatar etc.
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(sent[0].Data, &decoded))
	assert.Equal(t, "contact_name_changed", decoded["type"])
	assert.Equal(t, string(evt1.UUID()), decoded["uuid"])
	assert.Equal(t, map[string]any{"uuid": "eb9536d7-7b22-4ca6-9a1e-8e1f1effe7f3", "name": "Ann Admin"}, decoded["_user"])

	require.NoError(t, json.Unmarshal(sent[1].Data, &decoded))
	assert.Equal(t, "contact_language_changed", decoded["type"])
	assert.Equal(t, "spa", decoded["language"])
}
