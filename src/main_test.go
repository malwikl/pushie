package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/go-webauthn/webauthn/webauthn"
)

func testApp(t *testing.T) *application {
	t.Helper()
	db, err := openStore(filepath.Join(t.TempDir(), "webpush.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	wa, err := webauthn.New(&webauthn.Config{RPID: "localhost", RPDisplayName: "Test", RPOrigins: []string{"http://localhost:8099"}})
	if err != nil {
		t.Fatal(err)
	}
	return &application{db: db, wa: wa, auth: newAuthState(), cfg: config{origin: "http://localhost:8099", rpID: "localhost", adminToken: strings.Repeat("a", 64), sendToken: strings.Repeat("b", 64), publicKey: "public", privateKey: "private", subject: "mailto:test@example.com"}}
}
func request(t *testing.T, h http.Handler, method, path string, body any, origin, token, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.RemoteAddr = "127.0.0.1:10000"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestPushEndpoints(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		valid    bool
	}{
		{"https://web.push.apple.com/Q123", true},
		{"https://fcm.googleapis.com/fcm/send/123", true},
		{"https://updates.push.services.mozilla.com/wpush/v2/abc", true},
		{"https://web.push.apple.com.evil.example/Q123", false},
		{"http://web.push.apple.com/Q123", false},
		{"https://localhost/internal", false},
		{"https://web.push.apple.com:8443/Q123", false},
		{"https://user:pwd@web.push.apple.com/a", false},
	} {
		if got := validPushEndpoint(tc.endpoint); got != tc.valid {
			t.Errorf("endpoint %q got %t, want %t", tc.endpoint, got, tc.valid)
		}
	}
}
func dummySubscription(endpoint string) webpush.Subscription {
	auth := make([]byte, 16)
	dh := make([]byte, 65)
	dh[0] = 4
	_, _ = rand.Read(auth)
	_, _ = rand.Read(dh[1:])
	return webpush.Subscription{Endpoint: endpoint, Keys: webpush.Keys{Auth: base64.RawURLEncoding.EncodeToString(auth), P256dh: base64.RawURLEncoding.EncodeToString(dh)}}
}
func TestTopicRoutingAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "webpush.sqlite")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.update(func(d *database) error {
		d.Users["one"] = &user{ID: "one", Name: "First", Topics: []string{"backup"}}
		d.Users["two"] = &user{ID: "two", Name: "Second", Topics: []string{"system"}}
		d.Devices["d1"] = &device{ID: "d1", UserID: "one", Subscription: dummySubscription("https://web.push.apple.com/a")}
		d.Devices["d2"] = &device{ID: "d2", UserID: "one", Subscription: dummySubscription("https://web.push.apple.com/b")}
		d.Devices["d3"] = &device{ID: "d3", UserID: "two", Subscription: dummySubscription("https://web.push.apple.com/c")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	opened, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := opened.devicesFor("backup", "", ""); len(got) != 2 {
		t.Fatalf("backup recipients: %d", len(got))
	}
	if got := opened.devicesFor("", "two", ""); len(got) != 1 {
		t.Fatalf("user recipients: %d", len(got))
	}
	if got := opened.devicesFor("", "", "d2"); len(got) != 1 || got[0].ID != "d2" {
		t.Fatalf("device recipients: %+v", got)
	}
	if err := opened.pruneDevices([]string{"d1"}); err != nil {
		t.Fatal(err)
	}
	if len(opened.devicesFor("backup", "", "")) != 1 {
		t.Fatal("prune failed")
	}
}
func TestOriginAndPermissions(t *testing.T) {
	a := testApp(t)
	h := a.routes()
	if r := request(t, h, "POST", "/api/admin/topics", map[string]any{"name": "secret"}, "", "", ""); r.Code != 401 {
		t.Fatalf("admin unprotected: %d", r.Code)
	}
	if r := request(t, h, "GET", "/api/me", nil, "", "", ""); r.Code != 401 {
		t.Fatalf("profile unprotected: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/auth/login/begin", map[string]any{}, "https://evil.example", "", ""); r.Code != 403 {
		t.Fatalf("login origin bypass: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/send", map[string]any{"topic": "system", "body": "hello"}, "", "", ""); r.Code != 401 {
		t.Fatalf("send unprotected: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/admin/topics", map[string]any{"name": "alerts", "description": "All alerts"}, "", a.cfg.adminToken, ""); r.Code != 201 {
		t.Fatalf("topic create: %d %s", r.Code, r.Body.String())
	}
	if r := request(t, h, "POST", "/api/admin/topics", map[string]any{"name": "alerts"}, "", a.cfg.adminToken, ""); r.Code != 409 {
		t.Fatalf("duplicate topic: %d", r.Code)
	}
}
func TestUserTopicSelfService(t *testing.T) {
	a := testApp(t)
	if err := a.db.update(func(d *database) error { d.Users["u1"] = &user{ID: "u1", Name: "Alice"}; return nil }); err != nil {
		t.Fatal(err)
	}
	token, err := a.auth.newSession("u1")
	if err != nil {
		t.Fatal(err)
	}
	cookie := a.cookieName() + "=" + token
	h := a.routes()
	if r := request(t, h, "PUT", "/api/me/topics/backup", map[string]any{}, "", "", cookie); r.Code != 403 {
		t.Fatalf("missing origin accepted: %d", r.Code)
	}
	if r := request(t, h, "PUT", "/api/me/topics/backup", map[string]any{}, a.cfg.origin, "", cookie); r.Code != 200 {
		t.Fatalf("subscribe: %d %s", r.Code, r.Body.String())
	}
	if u := a.db.userByID("u1"); !hasString(u.Topics, "backup") {
		t.Fatal("subscription not stored")
	}
	if r := request(t, h, "DELETE", "/api/me/topics/backup", map[string]any{}, a.cfg.origin, "", cookie); r.Code != 200 {
		t.Fatalf("unsubscribe: %d", r.Code)
	}
	if u := a.db.userByID("u1"); hasString(u.Topics, "backup") {
		t.Fatal("unsubscribe not stored")
	}
	if r := request(t, h, "PUT", "/api/me/topics/unknown", map[string]any{}, a.cfg.origin, "", cookie); r.Code != 404 {
		t.Fatalf("unknown topic: %d", r.Code)
	}
}
func TestInvitations(t *testing.T) {
	a := testApp(t)
	h := a.routes()
	w := request(t, h, "POST", "/api/admin/invites", map[string]any{"name": "Alice"}, "", a.cfg.adminToken, "")
	if w.Code != 201 {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Invite string `json:"invite"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Invite) < 32 {
		t.Fatal("short invitation")
	}
	snap := a.db.snapshot()
	if snap.Invites[invitationHash(out.Invite)] == nil {
		t.Fatal("invite not persisted")
	}
	if strings.Contains(string(w.Body.Bytes()), a.cfg.adminToken) {
		t.Fatal("admin token leaked")
	}
	w = request(t, h, "POST", "/api/auth/register/begin", map[string]any{"invite": out.Invite}, a.cfg.origin, "", "")
	if w.Code != 200 {
		t.Fatalf("begin: %d %s", w.Code, w.Body.String())
	}
	var begin struct {
		CeremonyID string `json:"ceremonyId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &begin); err != nil {
		t.Fatal(err)
	}
	if begin.CeremonyID == "" {
		t.Fatal("no challenge")
	}
	if _, ok := a.auth.takeCeremony(begin.CeremonyID, "invite"); !ok {
		t.Fatal("ceremony not stored")
	}
	if _, ok := a.auth.takeCeremony(begin.CeremonyID, "invite"); ok {
		t.Fatal("ceremony replay")
	}
}
func TestSendRequiresTarget(t *testing.T) {
	a := testApp(t)
	h := a.routes()
	for _, b := range []map[string]any{{"body": "hello"}, {"body": "hello", "topic": "backup", "userId": "something"}} {
		if r := request(t, h, "POST", "/api/send", b, "", a.cfg.sendToken, ""); r.Code != 400 {
			t.Fatalf("invalid routing: %d %s", r.Code, r.Body.String())
		}
	}
	r := request(t, h, "POST", "/api/send", map[string]any{"topic": "backup", "body": "hello"}, "", a.cfg.sendToken, "")
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"recipients":0`)) {
		t.Fatalf("empty topic send: %d %s", r.Code, r.Body.String())
	}
}
func TestSessionExpiry(t *testing.T) {
	a := newAuthState()
	tok, err := a.newSession("u")
	if err != nil {
		t.Fatal(err)
	}
	if a.lookupSession(tok) != "u" {
		t.Fatal("session not active")
	}
	a.mu.Lock()
	a.sessions[sessionHash(tok)] = sessionInfo{"u", time.Now().Add(-time.Second)}
	a.mu.Unlock()
	if a.lookupSession(tok) != "" {
		t.Fatal("expired session active")
	}
}
func TestSubscriptionValidation(t *testing.T) {
	s := dummySubscription("https://web.push.apple.com/A")
	if !validSubscription(s) {
		t.Fatal("valid subscription rejected")
	}
	s.Keys.Auth = "abc"
	if validSubscription(s) {
		t.Fatal("invalid auth key accepted")
	}
}
func TestHealthz(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestDeviceRegistrationOwnership(t *testing.T) {
	a := testApp(t)
	err := a.db.update(func(d *database) error {
		d.Users["alice"] = &user{ID: "alice", Name: "Alice"}
		d.Users["bob"] = &user{ID: "bob", Name: "Bob"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	alice, err := a.auth.newSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := a.auth.newSession("bob")
	if err != nil {
		t.Fatal(err)
	}
	sub := dummySubscription("https://web.push.apple.com/Q123")
	body := map[string]any{"name": "My phone", "subscription": map[string]any{"endpoint": sub.Endpoint, "keys": sub.Keys, "expirationTime": nil}}
	h := a.routes()
	r := request(t, h, "POST", "/api/me/devices", body, a.cfg.origin, "", a.cookieName()+"="+alice)
	if r.Code != 201 {
		t.Fatalf("alice registration: %d %s", r.Code, r.Body.String())
	}
	r = request(t, h, "POST", "/api/me/devices", body, a.cfg.origin, "", a.cookieName()+"="+bob)
	if r.Code != 409 {
		t.Fatalf("cross-user device theft accepted: %d %s", r.Code, r.Body.String())
	}
	if len(a.db.devicesFor("", "alice", "")) != 1 || len(a.db.devicesFor("", "bob", "")) != 0 {
		t.Fatal("ownership corrupted")
	}
}
