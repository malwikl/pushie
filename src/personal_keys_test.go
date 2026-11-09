package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func createPersonalKey(t *testing.T, a *application, cookie string) (string, string) {
	t.Helper()
	r := request(t, a.routes(), "POST", "/api/me/keys", map[string]string{"name": "CLI"}, a.cfg.origin, "", cookie)
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
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

func TestPersonalKeyLifecycle(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	cookie := signIn(t, a, "alice")
	other := signIn(t, a, "bob")
	h := a.routes()
	for _, tc := range []struct {
		cookie, origin string
		want           int
	}{{"", a.cfg.origin, 401}, {cookie, "https://evil.example", 403}} {
		r := request(t, h, "POST", "/api/me/keys", map[string]string{"name": "CLI"}, tc.origin, "", tc.cookie)
		if r.Code != tc.want {
			t.Fatalf("unauthorized create: %d", r.Code)
		}
	}
	token, id := createPersonalKey(t, a, cookie)
	for _, path := range []string{"/api/me", "/api/me/keys"} {
		r := request(t, h, "GET", path, nil, "", "", cookie)
		if r.Code != 200 || strings.Contains(r.Body.String(), token) || strings.Contains(r.Body.String(), invitationHash(token)) {
			t.Fatalf("secret exposed: %s", r.Body.String())
		}
	}
	if r := request(t, h, "GET", "/api/me/keys", nil, "", "", other); strings.Contains(r.Body.String(), id) {
		t.Fatal("other user sees key")
	}
	if r := request(t, h, "DELETE", "/api/me/keys/"+id, map[string]string{}, a.cfg.origin, "", other); r.Code != 404 {
		t.Fatalf("cross account revocation: %d", r.Code)
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	if !personalKeyValid(reopened.userByID("alice"), invitationHash(token)) {
		t.Fatal("key not persisted")
	}
	if personalKeyValid(reopened.userByID("alice"), token) {
		t.Fatal("plaintext stored")
	}
	_ = reopened.Close()
	if r := request(t, h, "GET", "/api/me/keys", nil, "", token, ""); r.Code != 401 {
		t.Fatalf("token grants session: %d", r.Code)
	}
	if r := request(t, h, "DELETE", "/api/me/keys/"+id, map[string]string{}, a.cfg.origin, "", cookie); r.Code != 200 {
		t.Fatalf("revoke: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/send", map[string]string{"body": "test"}, "", token, ""); r.Code != 401 {
		t.Fatalf("revoked token: %d", r.Code)
	}
	reopened, err = openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if personalKeyValid(reopened.userByID("alice"), invitationHash(token)) {
		t.Fatal("revocation not persisted")
	}
}

func TestPersonalKeySendIsolation(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "admin", nil)
	addTestUser(t, a, "bob", "", nil)
	if err := a.db.update(func(db *database) error {
		db.Devices["alice-phone"] = &device{ID: "alice-phone", UserID: "alice", Subscription: dummySubscription("https://web.push.apple.com/alice")}
		db.Devices["bob-phone"] = &device{ID: "bob-phone", UserID: "bob", Subscription: dummySubscription("https://web.push.apple.com/bob")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	token, _ := createPersonalKey(t, a, signIn(t, a, "alice"))
	bobToken, _ := createPersonalKey(t, a, signIn(t, a, "bob"))
	h := a.routes()
	for _, tc := range []struct {
		body map[string]any
		want int
	}{
		{map[string]any{"body": "test", "userId": "bob"}, 403},
		{map[string]any{"body": "test", "topic": "backup"}, 403},
		{map[string]any{"body": "test", "broadcast": true}, 403},
		{map[string]any{"body": "test", "deviceId": "bob-phone"}, 404},
		{map[string]any{"body": "test", "userId": "alice", "deviceIds": []string{"bob-phone"}}, 404},
	} {
		r := request(t, h, "POST", "/api/send", tc.body, "", token, "")
		if r.Code != tc.want {
			t.Fatalf("scope %v: %d %s", tc.body, r.Code, r.Body.String())
		}
	}
	r := request(t, h, "POST", "/api/send", map[string]string{"body": "self"}, "", token, "")
	if r.Code != 200 {
		t.Fatalf("self send: %d %s", r.Code, r.Body.String())
	}
	var sent sendResult
	_ = json.Unmarshal(r.Body.Bytes(), &sent)
	if sent.InboxUsers != 1 || sent.Queued != 1 {
		t.Fatalf("targets: %+v", sent)
	}
	if len(a.db.snapshot().Messages["bob"]) != 0 {
		t.Fatal("delivered to other account")
	}
	if r := request(t, h, "GET", sent.StatusURL, nil, "", token, ""); r.Code != 200 {
		t.Fatalf("own status: %d", r.Code)
	}
	if r := request(t, h, "GET", sent.StatusURL, nil, "", bobToken, ""); r.Code != 404 {
		t.Fatalf("other status: %d", r.Code)
	}
	r = request(t, h, "POST", "/api/send", map[string]string{"body": "server", "userId": "alice"}, "", a.cfg.sendToken, "")
	if r.Code != 200 {
		t.Fatalf("server token regression: %d", r.Code)
	}
	_ = json.Unmarshal(r.Body.Bytes(), &sent)
	if r := request(t, h, "GET", sent.StatusURL, nil, "", token, ""); r.Code != 404 {
		t.Fatalf("unowned send status: %d", r.Code)
	}
}

func TestPersonalKeyQuotaAndValidation(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	cookie := signIn(t, a, "alice")
	h := a.routes()
	for _, name := range []string{"", strings.Repeat("a", 81)} {
		if r := request(t, h, "POST", "/api/me/keys", map[string]string{"name": name}, a.cfg.origin, "", cookie); r.Code != 400 {
			t.Fatalf("invalid name: %d", r.Code)
		}
	}
	for i := 0; i < 20; i++ {
		createPersonalKey(t, a, cookie)
	}
	if r := request(t, h, "POST", "/api/me/keys", map[string]string{"name": "overflow"}, a.cfg.origin, "", cookie); r.Code != 409 {
		t.Fatalf("quota: %d %s", r.Code, r.Body.String())
	}
	// Removing a key frees a slot.
	id := a.db.userByID("alice").APIKeys[0].ID
	request(t, h, "DELETE", fmt.Sprintf("/api/me/keys/%s", id), map[string]string{}, a.cfg.origin, "", cookie)
	createPersonalKey(t, a, cookie)
}

func TestPersonalKeyRetryNamespaces(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	alice, _ := createPersonalKey(t, a, signIn(t, a, "alice"))
	aliceSecond, _ := createPersonalKey(t, a, signIn(t, a, "alice"))
	bob, _ := createPersonalKey(t, a, signIn(t, a, "bob"))
	send := func(token, body string, want int) sendResult {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/send", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", "shared-retry-key-123")
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("retry: %d %s", w.Code, w.Body.String())
		}
		var result sendResult
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	first := send(alice, `{"body":"hello"}`, 200)
	replay := send(aliceSecond, `{"body":"hello"}`, 200)
	if first.MessageID != replay.MessageID {
		t.Fatal("same account did not replay")
	}
	send(alice, `{"body":"changed"}`, 409)
	other := send(bob, `{"body":"hello"}`, 200)
	server := send(a.cfg.sendToken, `{"body":"hello","userId":"alice"}`, 200)
	if first.MessageID == other.MessageID || first.MessageID == server.MessageID {
		t.Fatal("retry namespace leaked")
	}
	if len(a.db.snapshot().Messages["alice"]) != 2 || len(a.db.snapshot().Messages["bob"]) != 1 {
		t.Fatal("duplicate or cross account send")
	}
}
