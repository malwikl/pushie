package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSyncCommittedChangesAreScoped(t *testing.T) {
	a := testApp(t)
	if err := a.db.update(func(d *database) error {
		d.Users["one"] = &user{ID: "one"}
		d.Users["two"] = &user{ID: "two"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	one, stop := a.db.subscribe("one")
	defer stop()
	two, stop2 := a.db.subscribe("two")
	defer stop2()
	if err := a.db.update(func(d *database) error { d.Messages["one"] = []inboxMessage{{ID: "new"}}; return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-one.changed:
	default:
		t.Fatal("missing committed change")
	}
	select {
	case <-two.changed:
		t.Fatal("cross-user event")
	default:
	}
	a.db.update(func(d *database) error { d.Messages["one"][0].ReadAt = new(time.Time); return errors.New("rollback") })
	select {
	case <-one.changed:
		t.Fatal("rolled-back event")
	default:
	}
	a.db.update(func(d *database) error { now := time.Now(); d.Messages["one"][0].ReadAt = &now; return nil })
	select {
	case <-one.changed:
	default:
		t.Fatal("read state not synced")
	}
}
func TestSyncAuthenticationAndOrigin(t *testing.T) {
	a := testApp(t)
	if w := request(t, a.routes(), "GET", "/api/me/sync", nil, a.cfg.origin, "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	for _, origin := range []string{"", "https://other.example"} {
		r := httptest.NewRequest("GET", "/api/me/sync", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		a.syncSocket(w, r, &user{ID: "one"})
		if w.Code != 403 {
			t.Fatalf("origin %q: %d", origin, w.Code)
		}
	}
}

type syncHijacker struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (h syncHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, bufio.NewReadWriter(bufio.NewReader(h.conn), bufio.NewWriter(h.conn)), nil
}
func TestSyncSocketHandshakeAndEvent(t *testing.T) {
	a := testApp(t)
	a.db.update(func(d *database) error { d.Users["one"] = &user{ID: "one"}; return nil })
	token, err := a.auth.newSession("one")
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	r := httptest.NewRequest("GET", "/api/me/sync", nil)
	r.Header.Set("Origin", a.cfg.origin)
	r.Header.Set("Connection", "keep-alive, Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	r.AddCookie(&http.Cookie{Name: a.cookieName(), Value: token})
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.routes().ServeHTTP(syncHijacker{httptest.NewRecorder(), server}, r)
	}()
	reader := bufio.NewReader(client)
	response, err := http.ReadResponse(reader, r)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatal("invalid handshake")
	}
	readEvent := func() {
		t.Helper()
		op, _ := reader.ReadByte()
		n, _ := reader.ReadByte()
		p := make([]byte, int(n))
		if _, err := io.ReadFull(reader, p); err != nil {
			t.Fatal(err)
		}
		var event map[string]string
		if json.Unmarshal(p, &event) != nil || op != 0x81 || event["type"] != "sync" {
			t.Fatalf("invalid event %s", p)
		}
	}
	readEvent()
	if err := a.db.update(func(d *database) error { d.Messages["one"] = []inboxMessage{{ID: "new"}}; return nil }); err != nil {
		t.Fatal(err)
	}
	readEvent()
	client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("socket leaked")
	}
}
func TestSyncRejectsBadHandshake(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest("GET", "/api/me/sync", nil)
	r.Header.Set("Origin", a.cfg.origin)
	w := httptest.NewRecorder()
	a.syncSocket(w, r, &user{ID: "one"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "handshake") {
		t.Fatal(w.Code)
	}
}

func TestSyncCoalescesAndUnsubscribes(t *testing.T) {
	a := testApp(t)
	a.db.update(func(d *database) error { d.Users["one"] = &user{ID: "one"}; return nil })
	sub, stop := a.db.subscribe("one")
	a.db.update(func(d *database) error { return nil })
	select {
	case <-sub.changed:
		t.Fatal("no-op emitted an event")
	default:
	}
	for _, name := range []string{"First", "Second"} {
		if err := a.db.update(func(d *database) error { d.Users["one"].Name = name; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if len(sub.changed) != 1 {
		t.Fatal("changes did not coalesce")
	}
	stop()
	stop()
	a.db.mu.RLock()
	defer a.db.mu.RUnlock()
	if len(a.db.subscribers) != 0 {
		t.Fatal("subscriber leaked")
	}
}
func TestSyncControlFrames(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	wire := &syncWire{conn: server}
	done := make(chan struct{})
	go func() { defer close(done); defer server.Close(); wire.read(bufio.NewReader(server)) }()
	// A browser sends masked control frames; the server returns an unmasked pong.
	if _, err := client.Write([]byte{0x89, 0x81, 1, 2, 3, 4, 'x' ^ 1}); err != nil {
		t.Fatal(err)
	}
	p := make([]byte, 3)
	if _, err := io.ReadFull(client, p); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p, []byte{0x8a, 1, 'x'}) {
		t.Fatalf("invalid pong %v", p)
	}
	// Client messages without masking are rejected immediately.
	client.Write([]byte{0x89, 0})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("invalid frame accepted")
	}
}
