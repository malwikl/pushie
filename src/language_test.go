package main

import (
	"bytes"
	"testing"
)

func TestLanguageSetting(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	cookie := signIn(t, a, "alice")
	h := a.routes()
	for _, tc := range []struct {
		language, origin, cookie string
		code                     int
	}{
		{"en", a.cfg.origin, "", 401},
		{"en", "https://other.example", cookie, 403},
		{"fr", a.cfg.origin, cookie, 400},
		{"", a.cfg.origin, cookie, 400},
		{"en", a.cfg.origin, cookie, 200},
		{"de", a.cfg.origin, cookie, 200},
	} {
		r := request(t, h, "PUT", "/api/me/settings", map[string]string{"language": tc.language}, tc.origin, "", tc.cookie)
		if r.Code != tc.code {
			t.Fatalf("setting %q: %d %s", tc.language, r.Code, r.Body.String())
		}
	}
	r := request(t, h, "GET", "/api/me", nil, "", "", cookie)
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"language":"de"`)) {
		t.Fatalf("profile: %d %s", r.Code, r.Body.String())
	}
	if a.db.userByID("bob").Language != "" {
		t.Fatal("another user's preference changed")
	}
	opened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	if opened.userByID("alice").Language != "de" {
		t.Fatal("language not persisted")
	}
}

func TestThemeSetting(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	cookie := signIn(t, a, "alice")
	h := a.routes()
	request(t, h, "PUT", "/api/me/settings", map[string]string{"language": "en"}, a.cfg.origin, "", cookie)
	for _, tc := range []struct {
		theme, origin, cookie string
		code                  int
	}{
		{"dark", a.cfg.origin, "", 401}, {"dark", "https://other.example", cookie, 403},
		{"invalid", a.cfg.origin, cookie, 400}, {"", a.cfg.origin, cookie, 400},
		{"light", a.cfg.origin, cookie, 200}, {"dark", a.cfg.origin, cookie, 200}, {"auto", a.cfg.origin, cookie, 200},
	} {
		r := request(t, h, "PUT", "/api/me/settings", map[string]string{"theme": tc.theme}, tc.origin, "", tc.cookie)
		if r.Code != tc.code {
			t.Fatalf("theme %q: %d %s", tc.theme, r.Code, r.Body.String())
		}
	}
	if a.db.userByID("alice").Language != "en" {
		t.Fatal("theme update erased language")
	}
	request(t, h, "PUT", "/api/me/settings", map[string]string{"language": "de"}, a.cfg.origin, "", cookie)
	if a.db.userByID("alice").Theme != "" {
		t.Fatal("language update erased theme")
	}
	if a.db.userByID("bob").Theme != "" {
		t.Fatal("another user's theme changed")
	}
	r := request(t, h, "GET", "/api/me", nil, "", "", cookie)
	if !bytes.Contains(r.Body.Bytes(), []byte(`"theme":""`)) {
		t.Fatalf("theme missing from profile: %s", r.Body.String())
	}
	opened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	if opened.userByID("alice").Theme != "" {
		t.Fatal("auto should leave no saved theme")
	}
}
