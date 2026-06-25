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

func TestHistorySocket(t *testing.T) {
	contact := flows.ContactUUID("a393abc0-283d-4c9b-a1b3-641a035c34bf")
	ticket := flows.TicketUUID("019905d4-5f7b-71b8-bcb8-6a68de2d91d2")

	// with no ticket it's the contact's socket
	assert.Equal(t, "history:a393abc0-283d-4c9b-a1b3-641a035c34bf", models.HistorySocket(contact))

	// with a ticket it's that ticket's socket
	assert.Equal(t, "history:a393abc0-283d-4c9b-a1b3-641a035c34bf:019905d4-5f7b-71b8-bcb8-6a68de2d91d2", models.HistorySocket(contact, ticket))
}

func TestSubscribedSockets(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	vc := rt.VK.Get()
	defer vc.Close()

	contact1 := flows.ContactUUID("a393abc0-283d-4c9b-a1b3-641a035c34bf")
	contact2 := flows.ContactUUID("b699a406-7e44-49be-9f01-1a82893e8a10")
	hist1 := models.HistorySocket(contact1)
	hist2 := models.HistorySocket(contact2)

	// no sockets to check is a no-op
	subs, err := models.SubscribedSockets(ctx, rt)
	require.NoError(t, err)
	assert.Empty(t, subs)

	// nothing subscribed yet
	subs, err = models.SubscribedSockets(ctx, rt, hist1, hist2)
	require.NoError(t, err)
	assert.Empty(t, subs)

	// the authorizing service records a subscription by setting the per-socket presence key; mailroom only reads it
	_, err = vc.Do("SET", "socket-subs:"+hist1, "1")
	require.NoError(t, err)

	// a single lookup reports which of the requested sockets are subscribed
	subs, err = models.SubscribedSockets(ctx, rt, hist1, hist2)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{hist1: true}, subs)

	// once both are subscribed they both come back from the one lookup
	_, err = vc.Do("SET", "socket-subs:"+hist2, "1")
	require.NoError(t, err)
	subs, err = models.SubscribedSockets(ctx, rt, hist1, hist2)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{hist1: true, hist2: true}, subs)
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
	var requests int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()

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
	sentTo := func(socket string) []publish {
		var out []publish
		for _, p := range snapshot() {
			if p.Channel == socket {
				out = append(out, p)
			}
		}
		return out
	}
	reqCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}

	rt.Centrifugo = gocent.New(gocent.Config{Addr: srv.URL, Key: "sesame"})

	contact := flows.ContactUUID("a393abc0-283d-4c9b-a1b3-641a035c34bf")
	socket := models.HistorySocket(contact)
	evt1 := events.NewContactNameChanged("Bob")
	evt1.SetUser(assets.NewUserReference("eb9536d7-7b22-4ca6-9a1e-8e1f1effe7f3", "Ann Admin"), "ui")
	evt2 := events.NewContactLanguageChanged("spa")

	// socket isn't subscribed yet, so nothing is published
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, []flows.Event{evt1}))
	assert.Empty(t, snapshot())

	// mark the socket subscribed (as the authorizing service would) - empty event slice is still a no-op
	_, err := vc.Do("SET", "socket-subs:"+socket, "1")
	require.NoError(t, err)
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, nil))
	assert.Empty(t, snapshot())

	// now that it's subscribed, each event is published to the contact's history socket as its full JSON
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, []flows.Event{evt1, evt2}))

	sent := snapshot()
	require.Len(t, sent, 2)
	assert.Equal(t, socket, sent[0].Channel)
	assert.Equal(t, socket, sent[1].Channel)

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

	// per-ticket detail events (assignee/note/topic changes) route to that ticket's socket rather than the contact
	// socket, mirroring how the read API filters them off the contact page; the basic ticket lifecycle events
	// (opened/closed/reopened) and non-ticket events stay on the contact socket
	ticketA := flows.TicketUUID("019905d4-5f7b-71b8-bcb8-6a68de2d91d2")
	ticketB := flows.TicketUUID("28e94070-7c69-4f8e-9b7e-2c5a3a3e6f9a")
	ticketASocket := models.HistorySocket(contact, ticketA)
	ticketBSocket := models.HistorySocket(contact, ticketB)

	// ticket A's socket is subscribed (e.g. someone has its ticket page open), ticket B's is not
	_, err = vc.Do("SET", "socket-subs:"+ticketASocket, "1")
	require.NoError(t, err)

	closed := events.NewTicketClosed(ticketA)                                                                                        // basic lifecycle -> contact socket
	noteA := events.NewTicketNoteAdded(ticketA, "look at this")                                                                      // detail -> ticket A socket
	assignB := events.NewTicketAssigneeChanged(ticketB, assets.NewUserReference("0c78ef47-7d56-44d8-8f57-96e0f30e8f44", "Bob"), nil) // detail -> ticket B socket (unsubscribed)
	lang := events.NewContactLanguageChanged("fra")                                                                                  // non-ticket -> contact socket

	reqsBefore := reqCount()
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, []flows.Event{closed, noteA, assignB, lang}))

	// even though this commit spans two sockets (contact + ticket A), it's a single pipelined centrifugo round-trip
	assert.Equal(t, 1, reqCount()-reqsBefore)

	// the contact socket got the basic ticket event and the non-ticket event in order, plus the two from before
	contactSent := sentTo(socket)
	require.Len(t, contactSent, 4)
	require.NoError(t, json.Unmarshal(contactSent[2].Data, &decoded))
	assert.Equal(t, "ticket_closed", decoded["type"])
	require.NoError(t, json.Unmarshal(contactSent[3].Data, &decoded))
	assert.Equal(t, "contact_language_changed", decoded["type"])

	// ticket A's subscribed socket got only its own detail event, again as full JSON including its uuid
	ticketASent := sentTo(ticketASocket)
	require.Len(t, ticketASent, 1)
	require.NoError(t, json.Unmarshal(ticketASent[0].Data, &decoded))
	assert.Equal(t, "ticket_note_added", decoded["type"])
	assert.Equal(t, string(noteA.UUID()), decoded["uuid"])

	// ticket B's socket wasn't subscribed, so nothing was published to it
	assert.Empty(t, sentTo(ticketBSocket))

	// a commit can span several ticket sockets at once (e.g. a future bulk ticket operation) - subscribing ticket B
	// too, a publish touching both tickets and the contact resolves every socket's subscription in one lookup and is
	// still a single centrifugo round-trip
	_, err = vc.Do("SET", "socket-subs:"+ticketBSocket, "1")
	require.NoError(t, err)

	noteA2 := events.NewTicketNoteAdded(ticketA, "more on A") // detail -> ticket A socket
	noteB := events.NewTicketNoteAdded(ticketB, "now on B")   // detail -> ticket B socket
	renamed := events.NewContactNameChanged("Bobby")          // non-ticket -> contact socket

	reqsBefore = reqCount()
	require.NoError(t, models.PublishToHistory(ctx, rt, contact, []flows.Event{noteA2, noteB, renamed}))
	assert.Equal(t, 1, reqCount()-reqsBefore)

	// each socket received exactly its own events, across all three sockets in the one round-trip
	require.Len(t, sentTo(socket), 5) // evt1, evt2, closed, lang, renamed
	require.NoError(t, json.Unmarshal(sentTo(socket)[4].Data, &decoded))
	assert.Equal(t, "contact_name_changed", decoded["type"])

	ticketASent = sentTo(ticketASocket)
	require.Len(t, ticketASent, 2) // noteA, noteA2
	require.NoError(t, json.Unmarshal(ticketASent[1].Data, &decoded))
	assert.Equal(t, string(noteA2.UUID()), decoded["uuid"])

	ticketBSent := sentTo(ticketBSocket)
	require.Len(t, ticketBSent, 1) // noteB
	require.NoError(t, json.Unmarshal(ticketBSent[0].Data, &decoded))
	assert.Equal(t, string(noteB.UUID()), decoded["uuid"])
}
