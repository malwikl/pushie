package main

import (
	"encoding/json"
	"testing"
)

func TestUnifiedIntegrationKey(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "admin", "admin", nil)
	cookie := signIn(t, a, "admin")
	h := a.routes()
	w := request(t, h, "POST", "/api/admin/integration-keys", map[string]string{"name": "Home Assistant"}, a.cfg.origin, "", cookie)
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
	if r := request(t, h, "POST", "/api/send", map[string]string{"topic": "automation", "title": "Watchtower", "body": "Container updated"}, "", key.Token, ""); r.Code != 200 {
		t.Fatalf("watchtower: %d %s", r.Code, r.Body.String())
	}
	if r := request(t, h, "DELETE", "/api/admin/integration-keys/"+key.Key.ID, nil, a.cfg.origin, "", cookie); r.Code != 200 {
		t.Fatal("revoke failed")
	}
	if r := request(t, h, "POST", "/api/send", map[string]any{"broadcast": true, "body": "revoked"}, "", key.Token, ""); r.Code != 401 {
		t.Fatal("revoked key accepted")
	}
}

// Keys saved before scopes existed or with either old scope work with both APIs.
func TestExistingIntegrationKeysWorkWithBothAPIs(t *testing.T) {
	for _, scope := range []string{"", "watchtower", "send"} {
		t.Run("scope="+scope, func(t *testing.T) {
			a := testApp(t)
			token := "isk.existing-key"
			if err := a.db.update(func(db *database) error {
				db.IntegrationKeys = append(db.IntegrationKeys, personalAPIKey{ID: "existing", Name: "Existing", Scope: scope, Hash: invitationHash(token)})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			h := a.routes()
			for _, tc := range []struct {
				method, path string
				body         any
			}{
				{"GET", "/api/topics", nil},
				{"POST", "/api/verify", map[string]any{}},
				{"POST", "/api/send", map[string]any{"broadcast": true, "body": "Existing key"}},
				{"POST", "/api/send", map[string]string{"topic": "automation", "title": "Watchtower", "body": "Container updated"}},
			} {
				if r := request(t, h, tc.method, tc.path, tc.body, "", token, ""); r.Code != 200 {
					t.Fatalf("%s: %d %s", tc.path, r.Code, r.Body.String())
				}
			}
		})
	}
}

func TestLegacyKeyCreationScopesCreateUnifiedKeys(t *testing.T) {
	for _, scope := range []string{"watchtower", "send"} {
		t.Run(scope, func(t *testing.T) {
			a := testApp(t)
			addTestUser(t, a, "admin", "admin", nil)
			cookie := signIn(t, a, "admin")
			r := request(t, a.routes(), "POST", "/api/admin/integration-keys", map[string]string{"name": "Old client", "scope": scope}, a.cfg.origin, "", cookie)
			if r.Code != 201 {
				t.Fatalf("create: %d %s", r.Code, r.Body.String())
			}
			if keys := a.db.snapshot().IntegrationKeys; len(keys) != 1 || keys[0].Scope != "send" {
				t.Fatal("old client did not create a unified key")
			}
		})
	}
}
