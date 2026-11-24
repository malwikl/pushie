package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

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
	if r := request(t, h, "POST", "/api/send", map[string]string{"topic": "automation", "body": "test"}, "", token, ""); r.Code != 200 {
		t.Fatalf("integration key rejected on general send API: %d %s", r.Code, r.Body.String())
	}
	if r := request(t, h, "DELETE", "/api/admin/integration-keys/"+id, nil, a.cfg.origin, "", owner); r.Code != 200 {
		t.Fatal("revoke failed")
	}
	if r := request(t, h, "POST", "/api/send", map[string]string{"topic": "automation", "body": "test"}, "", token, ""); r.Code != 401 {
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
