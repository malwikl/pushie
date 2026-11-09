package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWatchtowerWebhook(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "subscriber", "", []string{"automation"})
	addTestUser(t, a, "other", "", nil)
	addTestUser(t, a, "watchtower-subscriber", "", []string{"watchtower"})
	addTestUser(t, a, "admin", "admin", nil)
	token, _ := createIntegrationKey(t, a, signIn(t, a, "admin"))
	h := a.routes()
	path := "/api/integrations/watchtower"
	send := func(token string, body any, want int) {
		t.Helper()
		r := request(t, h, "POST", path, body, "", token, "")
		if r.Code != want {
			t.Fatalf("status %d want %d: %s", r.Code, want, r.Body.String())
		}
	}
	send("", map[string]string{"message": "update"}, 401)
	send(a.cfg.sendToken, map[string]string{"message": "update"}, 401)
	send(a.cfg.adminToken, map[string]string{"message": "update"}, 401)
	send(token, map[string]string{"message": ""}, 400)
	send(token, map[string]string{"message": "update", "unknown": "x"}, 400)
	send(token, map[string]string{"message": "Container updated"}, 200)
	msgs := a.db.snapshot().Messages
	if len(msgs["subscriber"]) != 1 || msgs["subscriber"][0].Title != "Watchtower" || len(msgs["other"]) != 0 {
		t.Fatal("default topic routing failed")
	}
	if msgs["subscriber"][0].LongText != "Container updated" {
		t.Fatal("short report not preserved as long text")
	}
	long := strings.Repeat("✅ updated\n", 500)
	send(token, map[string]string{"title": "Updates", "message": long}, 200)
	saved := a.db.snapshot().Messages["subscriber"][1]
	if saved.LongText != long || len(saved.Body) > 2000 || saved.Title != "Updates" {
		t.Fatal("long report not preserved")
	}
	send(token, map[string]string{"message": strings.Repeat("x", 256*1024+1)}, 400)
	if r := request(t, h, "POST", path, map[string]string{"message": "update"}, "https://evil.example", token, ""); r.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	path += "?topic=missing"
	send(token, map[string]string{"message": "update"}, 404)
	path = "/api/integrations/watchtower?topic=automation&userId=other"
	send(token, map[string]string{"message": "update"}, 400)
	path = "/api/integrations/watchtower?userId=other"
	send(token, map[string]string{"message": "direct"}, 200)
	if !bytes.Contains([]byte(a.db.snapshot().Messages["other"][0].Body), []byte("direct")) {
		t.Fatal("direct target missing")
	}
	path = "/api/integrations/watchtower?topic=watchtower"
	if err := a.db.update(func(d *database) error {
		d.Topics["watchtower"] = &topic{Name: "watchtower"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	send(token, map[string]string{"message": "Container update details"}, 200)
	snap := a.db.snapshot()
	message := snap.Messages["watchtower-subscriber"][0]
	if message.Topic != "watchtower" || message.LongText != "Container update details" {
		t.Fatal("watchtower topic or full details missing")
	}
	if record := snap.MessageLog[message.ID]; record == nil || record.Target.Topic != "watchtower" || record.Notification.LongText != message.LongText || len(record.Recipients) != 1 {
		t.Fatal("watchtower audit record missing topic, full details or recipient")
	}
}

func TestWatchtowerPersonalKeyIsolation(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	token, _ := createPersonalKey(t, a, signIn(t, a, "alice"))
	h := a.routes()
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 200}, {"?topic=automation", 403}, {"?userId=bob", 403},
	} {
		r := request(t, h, "POST", "/api/integrations/watchtower"+tc.query, map[string]string{"message": "Watchtower update"}, "", token, "")
		if r.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.query, r.Code, r.Body.String())
		}
	}
	snap := a.db.snapshot()
	if len(snap.Messages["alice"]) != 1 || len(snap.Messages["bob"]) != 0 {
		t.Fatal("personal key isolation failed")
	}
}

func createIntegrationKey(t *testing.T, a *application, cookie string) (string, string) {
	t.Helper()
	r := request(t, a.routes(), "POST", "/api/admin/integration-keys", map[string]string{"name": "Watchtower"}, a.cfg.origin, "", cookie)
	if r.Code != 201 {
		t.Fatalf("create integration key: %d %s", r.Code, r.Body.String())
	}
	var result struct {
		Token string          `json:"token"`
		Key   personalKeyView `json:"key"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Token, result.Key.ID
}

func TestIntegrationKeyLifecycle(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "admin", "admin", nil)
	addTestUser(t, a, "member", "", nil)
	owner, member := signIn(t, a, "admin"), signIn(t, a, "member")
	token, id := createIntegrationKey(t, a, owner)
	h := a.routes()
	for _, method := range []string{"GET", "POST", "DELETE"} {
		path := "/api/admin/integration-keys"
		if method == "DELETE" {
			path += "/" + id
		}
		if r := request(t, h, method, path, map[string]string{"name": "x"}, a.cfg.origin, "", member); r.Code != 403 {
			t.Fatalf("nonadmin %s: %d", method, r.Code)
		}
	}
	if r := request(t, h, "POST", "/api/admin/integration-keys", map[string]string{"name": "x"}, "https://evil.example", "", owner); r.Code != 403 {
		t.Fatal("cross origin creation allowed")
	}
	r := request(t, h, "GET", "/api/admin/integration-keys", nil, "", "", owner)
	if r.Code != 200 || bytes.Contains(r.Body.Bytes(), []byte(token)) || bytes.Contains(r.Body.Bytes(), []byte("hash")) {
		t.Fatal("list exposes secret")
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	if !integrationKeyValid(reopened.snapshot().IntegrationKeys, invitationHash(token)) {
		t.Fatal("key not persisted")
	}
	reopened.sqlDB.Close()
	if r := request(t, h, "POST", "/api/send", map[string]string{"topic": "automation", "body": "test"}, "", token, ""); r.Code != 401 {
		t.Fatal("integration key accepted on general send API")
	}
	if r := request(t, h, "DELETE", "/api/admin/integration-keys/"+id, nil, a.cfg.origin, "", owner); r.Code != 200 {
		t.Fatal("revoke failed")
	}
	if r := request(t, h, "POST", "/api/integrations/watchtower", map[string]string{"message": "test"}, "", token, ""); r.Code != 401 {
		t.Fatal("revoked key accepted")
	}
	reopened, err = openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.sqlDB.Close()
	if len(reopened.snapshot().IntegrationKeys) != 0 {
		t.Fatal("revocation not persisted")
	}
}
