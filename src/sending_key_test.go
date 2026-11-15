package main

import (
	"encoding/json"
	"testing"
)

func TestAllSendingIntegrationKey(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "admin", "admin", nil)
	cookie := signIn(t, a, "admin")
	h := a.routes()
	w := request(t, h, "POST", "/api/admin/integration-keys", map[string]string{"name": "Home Assistant", "scope": "send"}, a.cfg.origin, "", cookie)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var key struct {
		Token string          `json:"token"`
		Key   personalKeyView `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/topics", "/api/verify"} {
		method := "GET"
		var body any
		if path == "/api/verify" {
			method = "POST"
			body = map[string]any{}
		}
		if r := request(t, h, method, path, body, "", key.Token, ""); r.Code != 200 {
			t.Fatalf("%s: %d %s", path, r.Code, r.Body.String())
		}
	}
	for _, target := range []map[string]any{{"broadcast": true}, {"userId": "admin"}} {
		target["body"] = "HA test"
		if r := request(t, h, "POST", "/api/send", target, "", key.Token, ""); r.Code != 200 {
			t.Fatalf("send: %d %s", r.Code, r.Body.String())
		}
	}
	if r := request(t, h, "GET", "/api/admin/users", nil, "", key.Token, ""); r.Code != 401 {
		t.Fatal("send key granted administration")
	}
	legacy, _ := createIntegrationKey(t, a, cookie)
	if r := request(t, h, "POST", "/api/verify", map[string]any{}, "", legacy, ""); r.Code != 401 {
		t.Fatal("legacy key gained privileges")
	}
	if r := request(t, h, "DELETE", "/api/admin/integration-keys/"+key.Key.ID, nil, a.cfg.origin, "", cookie); r.Code != 200 {
		t.Fatal("revoke failed")
	}
	if r := request(t, h, "POST", "/api/send", map[string]any{"broadcast": true, "body": "revoked"}, "", key.Token, ""); r.Code != 401 {
		t.Fatal("revoked key accepted")
	}
}
