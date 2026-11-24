package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUnifiedSendFullBodyAndIdempotency(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "admin", "admin", []string{"automation"})
	cookie := signIn(t, a, "admin")
	token, _ := createIntegrationKey(t, a, cookie)
	full := strings.Repeat("✅ Updated container\n", 1000)
	payload := map[string]string{"topic": "automation", "title": "Watchtower", "body": full}
	h := a.routes()
	send := func(body any) sendResult {
		t.Helper()
		r := request(t, h, "POST", "/api/send", body, "", token, "", "Idempotency-Key", "unified-report-0001")
		if r.Code != 200 {
			t.Fatalf("send: %d %s", r.Code, r.Body.String())
		}
		var result sendResult
		if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := send(payload)
	second := send(payload)
	if first.NotificationID != second.NotificationID {
		t.Fatal("retry created another notification")
	}
	saved := a.db.snapshot().Notifications["admin"]
	if len(saved) != 1 || saved[0].LongText != full || len(saved[0].Body) > 2000 || !utf8.ValidString(saved[0].Body) {
		t.Fatal("full text or preview incorrect")
	}
	payload["body"] = full + "changed"
	if r := request(t, h, "POST", "/api/send", payload, "", token, "", "Idempotency-Key", "unified-report-0001"); r.Code != 409 {
		t.Fatalf("changed retry: %d", r.Code)
	}
}

func TestUnifiedSendSchemaAndLimits(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "subscriber", "", []string{"automation"})
	for _, tc := range []struct {
		body map[string]any
		want int
	}{
		{map[string]any{"topic": "automation", "body": "Short"}, 200},
		{map[string]any{"topic": "automation", "body": strings.Repeat("x", 256*1024)}, 200},
		{map[string]any{"topic": "automation", "body": strings.Repeat("x", 256*1024+1)}, 400},
		{map[string]any{"body": "Missing target"}, 400},
		{map[string]any{"topic": "automation", "broadcast": true, "body": "Two targets"}, 400},
		{map[string]any{"topic": "automation", "message": "Old format"}, 400},
		{map[string]any{"topic": "automation", "body": "Short", "longText": "Old format"}, 400},
		{map[string]any{"topic": "automation", "body": " "}, 400},
	} {
		r := request(t, a.routes(), "POST", "/api/send", tc.body, "", a.cfg.sendToken, "")
		if r.Code != tc.want {
			t.Fatalf("status %d want %d: %s", r.Code, tc.want, r.Body.String())
		}
	}
	saved := a.db.snapshot().Notifications["subscriber"]
	if len(saved) != 2 || saved[0].Body != "Short" || saved[0].Title != "Push" || saved[0].LongText != "" {
		t.Fatal("short notification altered")
	}
}

func TestUnifiedSendPersonalKeyIsolation(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	token, _ := createPersonalKey(t, a, signIn(t, a, "alice"))
	for _, tc := range []struct {
		body map[string]any
		want int
	}{
		{map[string]any{"body": "Mine"}, 200},
		{map[string]any{"topic": "automation", "body": "Topic"}, 403},
		{map[string]any{"userId": "bob", "body": "Other"}, 403},
		{map[string]any{"broadcast": true, "body": "All"}, 403},
	} {
		r := request(t, a.routes(), "POST", "/api/send", tc.body, "", token, "")
		if r.Code != tc.want {
			t.Fatalf("personal: %d want %d", r.Code, tc.want)
		}
	}
	if len(a.db.snapshot().Notifications["alice"]) != 1 || len(a.db.snapshot().Notifications["bob"]) != 0 {
		t.Fatal("personal isolation changed")
	}
}
