package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/go-webauthn/webauthn/webauthn"
	"strings"
	"testing"
	"time"
)

func signIn(t *testing.T, a *application, id string) string {
	t.Helper()
	tok, err := a.auth.newSession(id)
	if err != nil {
		t.Fatal(err)
	}
	return a.cookieName() + "=" + tok
}
func addTestUser(t *testing.T, a *application, id, role string, topics []string) {
	t.Helper()
	if err := a.db.update(func(d *database) error {
		d.Users[id] = &user{ID: id, Name: id, Role: role, Topics: topics}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestBootstrapOneTimeConfigAndRole(t *testing.T) {
	a := testApp(t)
	a.cfg.bootstrapOTP = strings.Repeat("s", 48)
	h := a.routes()
	if !a.db.bootstrapAvailable(a.cfg.bootstrapOTP) {
		t.Fatal("expected bootstrap available")
	}
	if r := request(t, h, "POST", "/api/auth/bootstrap/begin", map[string]string{"name": "Owner", "code": "wrong"}, a.cfg.origin, "", ""); r.Code != 403 {
		t.Fatalf("invalid code: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/auth/bootstrap/begin", map[string]string{"name": "Owner", "code": a.cfg.bootstrapOTP}, "https://evil.example", "", ""); r.Code != 403 {
		t.Fatalf("foreign origin: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/auth/bootstrap/begin", map[string]string{"name": "Owner", "code": a.cfg.bootstrapOTP}, a.cfg.origin, "", ""); r.Code != 200 {
		t.Fatalf("bootstrap begin: %d %s", r.Code, r.Body.String())
	}
	if err := a.db.registerBootstrapAdmin(&user{ID: "admin", Name: "Owner", Credentials: []webauthn.Credential{{ID: []byte("passkey")}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.db.registerBootstrapAdmin(&user{ID: "second", Credentials: []webauthn.Credential{{ID: []byte("passkey2")}}}); err == nil {
		t.Fatal("bootstrap replay succeeded")
	}
	if a.db.bootstrapAvailable(a.cfg.bootstrapOTP) {
		t.Fatal("bootstrap available after use")
	}
	if r := request(t, h, "POST", "/api/auth/bootstrap/begin", map[string]string{"name": "Owner", "code": a.cfg.bootstrapOTP}, a.cfg.origin, "", ""); r.Code != 403 {
		t.Fatalf("bootstrap re-use: %d", r.Code)
	}
	opened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	if opened.bootstrapAvailable(a.cfg.bootstrapOTP) || !opened.userByID("admin").isAdmin() {
		t.Fatal("bootstrap state lost after reload")
	}
}
func TestWebAdminAndInboxIsolation(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "admin", []string{"backup"})
	addTestUser(t, a, "bob", "", []string{"backup"})
	addTestUser(t, a, "carol", "", []string{"system"})
	adminCookie := signIn(t, a, "alice")
	bobCookie := signIn(t, a, "bob")
	carolCookie := signIn(t, a, "carol")
	h := a.routes()
	if r := request(t, h, "POST", "/api/admin/send", map[string]string{"topic": "backup", "body": "test"}, a.cfg.origin, "", bobCookie); r.Code != 403 {
		t.Fatalf("non-admin: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/admin/send", map[string]string{"topic": "backup", "body": "test"}, "", "", adminCookie); r.Code != 403 {
		t.Fatalf("missing Origin: %d", r.Code)
	}
	if r := request(t, h, "GET", "/api/admin/targets", nil, "", "", bobCookie); r.Code != 403 {
		t.Fatalf("targets leaked: %d", r.Code)
	}
	r := request(t, h, "POST", "/api/admin/send", map[string]string{"topic": "backup", "title": "Backup ready", "body": "Job done"}, a.cfg.origin, "", adminCookie)
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"inboxUsers":2`)) {
		t.Fatalf("admin send: %d %s", r.Code, r.Body.String())
	}
	for _, tc := range []struct {
		user, cookie string
		count        int
	}{{"alice", adminCookie, 1}, {"bob", bobCookie, 1}, {"carol", carolCookie, 0}} {
		rr := request(t, h, "GET", "/api/me/messages", nil, "", "", tc.cookie)
		if rr.Code != 200 {
			t.Fatalf("inbox %s: %d", tc.user, rr.Code)
		}
		var v struct {
			Messages []inboxMessage `json:"messages"`
			Unread   int            `json:"unread"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if len(v.Messages) != tc.count || v.Unread != tc.count {
			t.Fatalf("inbox %s: %d vs %d", tc.user, len(v.Messages), tc.count)
		}
	}
	inbox := a.db.snapshot().Messages["bob"]
	id := inbox[0].ID
	if r := request(t, h, "PUT", fmt.Sprintf("/api/me/messages/%s/read", id), map[string]string{}, a.cfg.origin, "", carolCookie); r.Code != 404 {
		t.Fatalf("cross-account read: %d", r.Code)
	}
	if r := request(t, h, "PUT", fmt.Sprintf("/api/me/messages/%s/read", id), map[string]string{}, a.cfg.origin, "", bobCookie); r.Code != 200 {
		t.Fatalf("read own message: %d", r.Code)
	}
	if r := request(t, h, "DELETE", fmt.Sprintf("/api/me/messages/%s", id), nil, a.cfg.origin, "", carolCookie); r.Code != 404 {
		t.Fatalf("cross-account delete: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/me/messages/read-all", map[string]string{}, a.cfg.origin, "", adminCookie); r.Code != 200 {
		t.Fatalf("read-all: %d", r.Code)
	}
	reloaded, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.snapshot().Messages["alice"][0].ReadAt == nil || reloaded.snapshot().Messages["bob"][0].ReadAt == nil {
		t.Fatal("read receipt not persisted")
	}
}
func TestExternalAPIWithNoPushDeviceAndRetention(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "target", "", []string{"system"})
	h := a.routes()
	r := request(t, h, "POST", "/api/send", map[string]string{"userId": "target", "title": "Direct", "body": "External notification"}, "", a.cfg.sendToken, "")
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"inboxUsers":1`)) {
		t.Fatalf("external send: %d %s", r.Code, r.Body.String())
	}
	if !strings.Contains(a.db.snapshot().Messages["target"][0].Body, "External") {
		t.Fatal("message missing")
	}
	if r := request(t, h, "POST", "/api/send", map[string]string{"userId": "target", "body": "Hello"}, "https://evil.example", a.cfg.sendToken, ""); r.Code != 403 {
		t.Fatalf("browser Origin accepted for token API: %d", r.Code)
	}
	if err := a.db.update(func(d *database) error {
		list := d.Messages["target"]
		for len(list) < maxInboxMessages {
			list = append(list, inboxMessage{ID: fmt.Sprintf("%d", len(list)), Title: "X", CreatedAt: time.Now()})
		}
		d.Messages["target"] = list
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r = request(t, h, "POST", "/api/send", map[string]string{"userId": "target", "body": "newest"}, "", a.cfg.sendToken, "")
	if r.Code != 200 {
		t.Fatalf("retention trigger: %d", r.Code)
	}
	msgs := a.db.snapshot().Messages["target"]
	if len(msgs) != maxInboxMessages || msgs[0].ID == "0" || msgs[len(msgs)-1].Body != "newest" {
		t.Fatalf("retention not applied: %d", len(msgs))
	}
}
func TestLegacyAdminTokenCanPromote(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "legacy", "", nil)
	h := a.routes()
	r := request(t, h, "POST", "/api/admin/users/legacy/promote", map[string]string{}, "", a.cfg.adminToken, "")
	if r.Code != 200 || !a.db.userByID("legacy").isAdmin() {
		t.Fatalf("migration failed: %d %s", r.Code, r.Body.String())
	}
	r = request(t, h, "POST", "/api/admin/users/legacy/promote", map[string]string{}, "", "", "")
	if r.Code != 401 {
		t.Fatalf("promotion unauthenticated: %d", r.Code)
	}
}
