package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCentralMessageLogPersistenceFiltersAndAccess(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "admin", nil)
	addTestUser(t, a, "bob", "", nil)
	admin, ordinary := signIn(t, a, "alice"), signIn(t, a, "bob")
	h := a.routes()
	long := strings.Repeat("long text line\n", 2000) + "needle-in-long-text"
	r := request(t, h, "POST", "/api/admin/send", map[string]string{"userId": "bob", "title": "Audit", "body": "Summary", "longText": long}, a.cfg.origin, "", admin)
	if r.Code != 200 {
		t.Fatalf("send: %d %s", r.Code, r.Body.String())
	}
	var sent sendResult
	json.Unmarshal(r.Body.Bytes(), &sent)
	for _, cookie := range []string{ordinary, ""} {
		for _, path := range []string{"/api/admin/message-log", "/api/admin/message-log/" + sent.MessageID} {
			r = request(t, h, "GET", path, nil, "", "", cookie)
			if r.Code != 401 && r.Code != 403 {
				t.Fatalf("access allowed: %d", r.Code)
			}
		}
	}
	for _, tc := range []struct {
		query string
		count int
	}{
		{"", 1}, {"?q=needle-in-long-text", 1}, {"?sender=alice&recipient=bob&status=no_devices", 1},
		{"?sender=bob", 0}, {"?recipient=absent", 0}, {"?status=confirmed", 0}, {"?from=2000-01-01&to=2000-12-31", 0},
	} {
		r = request(t, h, "GET", "/api/admin/message-log"+tc.query, nil, "", "", admin)
		var response struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil || r.Code != 200 || response.Total != tc.count {
			t.Fatalf("filter %s: %d %s", tc.query, r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), "needle-in-long-text") {
			t.Fatal("list includes full long text")
		}
	}
	if err := a.db.update(func(d *database) error { delete(d.Messages, "bob"); return nil }); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	a.db = reopened
	r = request(t, a.routes(), "GET", "/api/admin/message-log/"+sent.MessageID, nil, "", "", admin)
	var record messageLogRecord
	if err := json.Unmarshal(r.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || record.Notification.LongText != long || record.Sender.ID != "alice" || record.Target.UserID != "bob" || len(record.Recipients) != 1 {
		t.Fatalf("record lost: %d", r.Code)
	}
}

func TestCentralLogMigrationAndDeliveryRetention(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "bob", "", nil)
	if err := a.db.update(func(d *database) error {
		d.Messages["bob"] = []inboxMessage{{ID: "historic", Title: "Old", Body: "body", LongText: "preserved"}}
		d.Deliveries["job"] = &delivery{ID: "job", MessageID: "historic", UserID: "bob", DeviceID: "phone", Status: "retry", ReceiptSecret: "private"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate a version 1 installation with existing inbox rows.
	if _, err := a.db.sqlDB.Exec("DROP TABLE message_log"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.sqlDB.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	migrated, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	record := migrated.snapshot().MessageLog["historic"]
	if record == nil || !record.Recovered || record.Notification.LongText != "preserved" || len(record.Deliveries) != 1 {
		t.Fatal("migration did not recover history")
	}
	if err := migrated.update(func(d *database) error { d.Deliveries["job"].Status = "confirmed"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := migrated.update(func(d *database) error { delete(d.Deliveries, "job"); delete(d.Messages, "bob"); return nil }); err != nil {
		t.Fatal(err)
	}
	record = migrated.snapshot().MessageLog["historic"]
	if record.Deliveries[0].Status != "confirmed" {
		t.Fatal("delivery history lost")
	}
	payload, _ := json.Marshal(record)
	if strings.Contains(string(payload), "private") {
		t.Fatal("receipt secret exposed")
	}
}

func TestMessageLogSettingsAndCleanup(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "admin", "admin", nil)
	addTestUser(t, a, "member", "", nil)
	admin, member := signIn(t, a, "admin"), signIn(t, a, "member")
	h := a.routes()
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/admin/message-log/settings", nil},
		{"PUT", "/api/admin/message-log/settings", map[string]bool{"enabled": false}},
		{"POST", "/api/admin/message-log/cleanup-preview", map[string]int{"days": 0}},
		{"POST", "/api/admin/message-log/cleanup", map[string]any{"days": 0, "confirm": "LÖSCHEN"}},
	} {
		if r := request(t, h, tc.method, tc.path, tc.body, a.cfg.origin, "", member); r.Code != 403 {
			t.Fatalf("member access: %s %d", tc.path, r.Code)
		}
		if tc.method != "GET" {
			if r := request(t, h, tc.method, tc.path, tc.body, "https://evil.example", "", admin); r.Code != 403 {
				t.Fatalf("cross-origin access: %s %d", tc.path, r.Code)
			}
		}
	}
	send := func() {
		t.Helper()
		r := request(t, h, "POST", "/api/admin/send", map[string]string{"userId": "member", "body": "details"}, a.cfg.origin, "", admin)
		if r.Code != 200 {
			t.Fatalf("send: %s", r.Body.String())
		}
	}
	send()
	if r := request(t, h, "PUT", "/api/admin/message-log/settings", map[string]bool{"enabled": false}, a.cfg.origin, "", admin); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	send()
	if len(a.db.snapshot().MessageLog) != 1 || len(a.db.snapshot().Messages["member"]) != 2 {
		t.Fatal("disabled log affected delivery or wrote log")
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.snapshot().MessageLogDisabled {
		t.Fatal("setting not persisted")
	}
	if r := request(t, h, "POST", "/api/admin/message-log/cleanup-preview", map[string]int{"days": 0}, a.cfg.origin, "", admin); r.Code != 200 || !strings.Contains(r.Body.String(), `"entries":1`) {
		t.Fatal(r.Body.String())
	}
	if len(a.db.snapshot().MessageLog) != 1 {
		t.Fatal("preview deleted log")
	}
	for _, days := range []int{-1, 3651} {
		if r := request(t, h, "POST", "/api/admin/message-log/cleanup", map[string]any{"days": days, "confirm": "LÖSCHEN"}, a.cfg.origin, "", admin); r.Code != 400 {
			t.Fatal("invalid days accepted")
		}
	}
	if r := request(t, h, "POST", "/api/admin/message-log/cleanup", map[string]int{"days": 0}, a.cfg.origin, "", admin); r.Code != 400 {
		t.Fatal("missing confirmation accepted")
	}
	if r := request(t, h, "POST", "/api/admin/message-log/cleanup", map[string]any{"days": 0, "confirm": "LÖSCHEN"}, a.cfg.origin, "", admin); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if len(a.db.snapshot().MessageLog) != 0 || len(a.db.snapshot().Messages["member"]) != 2 {
		t.Fatal("cleanup affected inbox or kept log")
	}
	if r := request(t, h, "PUT", "/api/admin/message-log/settings", map[string]bool{"enabled": true}, a.cfg.origin, "", admin); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	send()
	reopened2, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened2.Close()
	if len(reopened2.snapshot().MessageLog) != 1 || reopened2.snapshot().MessageLogDisabled {
		t.Fatal("deleted logs restored or logging not reenabled")
	}
}
