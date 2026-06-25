package models_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/centrifugal/gocent/v3"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryChannel(t *testing.T) {
	assert.Equal(t, "history:a393abc0-283d-4c9b-a1b3-641a035c34bf", models.HistoryChannel("a393abc0-283d-4c9b-a1b3-641a035c34bf"))
}

func TestTicketHistoryChannel(t *testing.T) {
	assert.Equal(t,
		"history:a393abc0-283d-4c9b-a1b3-641a035c34bf:019905d4-5f7b-71b8-bcb8-6a68de2d91d2",
		models.TicketHistoryChannel("a393abc0-283d-4c9b-a1b3-641a035c34bf", "019905d4-5f7b-71b8-bcb8-6a68de2d91d2"),
	)
}

func TestIsSubscribed(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	vc := rt.VK.Get()
	defer vc.Close()

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

	// the authorizing service records a subscription by setting the per-channel presence key; mailroom only reads it
	_, err := vc.Do("SET", "socket-subs:"+hist1, "1")
	require.NoError(t, err)
	assertSubscribed(hist1, true)

	// each channel is its own key, checked independently
	assertSubscribed(hist2, false)
	_, err = vc.Do("SET", "socket-subs:"+hist2, "1")
	require.NoError(t, err)
	assertSubscribed(hist2, true)
}

func TestPublishToHistory(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	vc := rt.VK.Get()
	defer vc.Close()

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

	rt.Centrifugo = gocent.New(gocent.Config{Addr: srv.URL, Key: "sesame"})

	contact := flows.ContactUUID("a393abc0-283d-4c9b-a1b3-641a035c34bf")
	channel := models.HistoryChannel(contact)
	evt1 := events.NewContactNameChanged("Bob")
	evt1.SetUser(assets.NewUserReference("eb9536d7-7b22-4ca6-9a1e-8e1f1effe7f3", "Ann Admin"), "ui")
	evt2 := events.NewContactLanguageChanged("spa")

	// channel isn't subscribed yet, so nothing is published
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, []flows.Event{evt1}))
	assert.Empty(t, snapshot())

	// mark the channel subscribed (as the authorizing service would) - empty event slice is still a no-op
	_, err := vc.Do("SET", "socket-subs:"+channel, "1")
	require.NoError(t, err)
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

	// per-ticket detail events (assignee/note/topic changes) route to that ticket's channel rather than the contact
	// channel, mirroring how the read API filters them off the contact page; the basic ticket lifecycle events
	// (opened/closed/reopened) and non-ticket events stay on the contact channel
	ticketA := flows.TicketUUID("019905d4-5f7b-71b8-bcb8-6a68de2d91d2")
	ticketB := flows.TicketUUID("28e94070-7c69-4f8e-9b7e-2c5a3a3e6f9a")
	ticketAChannel := models.TicketHistoryChannel(contact, ticketA)
	ticketBChannel := models.TicketHistoryChannel(contact, ticketB)

	// ticket A's channel is subscribed (e.g. someone has its ticket page open), ticket B's is not
	_, err = vc.Do("SET", "socket-subs:"+ticketAChannel, "1")
	require.NoError(t, err)

	sentTo := func(ch string) []publish {
		var out []publish
		for _, p := range snapshot() {
			if p.Channel == ch {
				out = append(out, p)
			}
		}
		return out
	}
	typeOf := func(data json.RawMessage) string {
		var e struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(data, &e))
		return e.Type
	}

	closed := events.NewTicketClosed(ticketA)                                                                                        // basic lifecycle -> contact channel
	noteA := events.NewTicketNoteAdded(ticketA, "look at this")                                                                      // detail -> ticket A channel
	assignB := events.NewTicketAssigneeChanged(ticketB, assets.NewUserReference("0c78ef47-7d56-44d8-8f57-96e0f30e8f44", "Bob"), nil) // detail -> ticket B channel (unsubscribed)
	lang := events.NewContactLanguageChanged("fra")                                                                                  // non-ticket -> contact channel

	require.NoError(t, models.PublishToHistory(ctx, rt, contact, []flows.Event{closed, noteA, assignB, lang}))

	// the contact channel got the basic ticket event and the non-ticket event in order, plus the two from before
	contactSent := sentTo(channel)
	require.Len(t, contactSent, 4)
	assert.Equal(t, "ticket_closed", typeOf(contactSent[2].Data))
	assert.Equal(t, "contact_language_changed", typeOf(contactSent[3].Data))

	// ticket A's subscribed channel got only its own detail event, again as full JSON including its uuid
	ticketASent := sentTo(ticketAChannel)
	require.Len(t, ticketASent, 1)
	require.NoError(t, json.Unmarshal(ticketASent[0].Data, &decoded))
	assert.Equal(t, "ticket_note_added", decoded["type"])
	assert.Equal(t, string(noteA.UUID()), decoded["uuid"])

	// ticket B's channel wasn't subscribed, so nothing was published to it
	assert.Empty(t, sentTo(ticketBChannel))
}
