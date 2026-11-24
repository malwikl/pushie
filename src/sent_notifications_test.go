package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSentNotificationsOwnershipAndDeliveryDetails(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "admin", nil)
	addTestUser(t, a, "bob", "", nil)
	addTestUser(t, a, "other", "admin", nil)
	alice, bob, other := signIn(t, a, "alice"), signIn(t, a, "bob"), signIn(t, a, "other")
	h := a.routes()
	r := request(t, h, "POST", "/api/admin/send", map[string]string{"userId": "bob", "title": "Sent title", "body": "Sent body"}, a.cfg.origin, "", alice)
	if r.Code != 202 && r.Code != 200 {
		t.Fatalf("send: %d %s", r.Code, r.Body.String())
	}
	var sent sendResult
	if err := json.Unmarshal(r.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if err := a.db.update(func(d *database) error {
		d.Deliveries["job"] = &delivery{ID: "job", NotificationID: sent.NotificationID, SenderUserID: "alice", UserID: "bob", DeviceID: "phone", DeviceName: "Bob phone", Status: "retry", Attempts: 2, LastError: "HTTP 503", ReceiptSecret: "secret-not-for-api"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cookie string
		own    bool
	}{{alice, true}, {bob, false}, {other, false}} {
		r = request(t, h, "GET", "/api/me/sent-notifications", nil, "", "", tc.cookie)
		if r.Code != 200 {
			t.Fatalf("list: %d %s", r.Code, r.Body.String())
		}
		var result struct {
			Notifications []struct {
				inboxNotification
				Deliveries []deliveryView `json:"deliveries"`
			} `json:"notifications"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if tc.own {
			if len(result.Notifications) != 1 || result.Notifications[0].ID != sent.NotificationID || len(result.Notifications[0].Deliveries) != 1 || result.Notifications[0].Deliveries[0].LastError != "HTTP 503" {
				t.Fatalf("missing sent details: %s", r.Body.String())
			}
		} else if len(result.Notifications) != 0 {
			t.Fatalf("another account sees sent notifications: %s", r.Body.String())
		}
		if strings.Contains(r.Body.String(), "secret-not-for-api") {
			t.Fatal("receipt secret exposed")
		}
	}
	if r = request(t, h, "GET", "/api/me/sent-notifications", nil, "", "", ""); r.Code != 401 {
		t.Fatalf("unauthenticated: %d", r.Code)
	}
}

func TestDeletedOwnNotificationDoesNotReappearInSentHistory(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "admin", []string{"shared"})
	addTestUser(t, a, "bob", "", []string{"shared"})
	if err := a.db.update(func(d *database) error {
		d.Topics["shared"] = &topic{Name: "shared"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	alice, bob := signIn(t, a, "alice"), signIn(t, a, "bob")
	h := a.routes()
	send := func() string {
		t.Helper()
		r := request(t, h, "POST", "/api/admin/send", map[string]string{"topic": "shared", "body": "test"}, a.cfg.origin, "", alice)
		if r.Code != 200 {
			t.Fatalf("send: %d %s", r.Code, r.Body.String())
		}
		var result sendResult
		if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.NotificationID
	}
	oldID := send()
	if err := a.db.update(func(d *database) error {
		d.Deliveries["retained"] = &delivery{ID: "retained", NotificationID: oldID, SenderUserID: "alice", UserID: "bob", Status: "pending"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A recipient deleting their copy must not hide the sender's history.
	r := request(t, h, "DELETE", "/api/me/notifications/"+oldID, nil, a.cfg.origin, "", bob)
	if r.Code != 200 {
		t.Fatalf("recipient delete: %d", r.Code)
	}
	r = request(t, h, "GET", "/api/me/sent-notifications", nil, "", "", alice)
	if !strings.Contains(r.Body.String(), oldID) {
		t.Fatal("recipient deletion removed sender history")
	}
	// Restore a recipient copy to exercise both reconstruction sources.
	if err := a.db.update(func(d *database) error {
		d.Notifications["bob"] = append(d.Notifications["bob"], d.Notifications["alice"][0])
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r = request(t, h, "DELETE", "/api/me/notifications/"+oldID, nil, a.cfg.origin, "", alice)
	if r.Code != 200 {
		t.Fatalf("sender delete: %d", r.Code)
	}
	newID := send()
	check := func() {
		t.Helper()
		r := request(t, h, "GET", "/api/me/sent-notifications", nil, "", "", alice)
		if r.Code != 200 || strings.Contains(r.Body.String(), oldID) || !strings.Contains(r.Body.String(), newID) {
			t.Fatalf("deleted notification resurrected or new notification missing: %s", r.Body.String())
		}
		r = request(t, h, "GET", "/api/me/notifications", nil, "", "", bob)
		if !strings.Contains(r.Body.String(), oldID) {
			t.Fatal("sender deletion removed recipient copy")
		}
		if job := a.db.snapshot().Deliveries["retained"]; job == nil || job.Status != "pending" {
			t.Fatal("sender deletion disrupted delivery")
		}
	}
	check()
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.sqlDB.Close()
	a.db = reopened
	check()
}

func TestSentHistoryDeletionAndCleanup(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "admin", nil)
	addTestUser(t, a, "bob", "", nil)
	alice, bob := signIn(t, a, "alice"), signIn(t, a, "bob")
	if err := a.db.update(func(db *database) error {
		db.Notifications["bob"] = []inboxNotification{{ID: "one", SenderUserID: "alice"}, {ID: "two", SenderUserID: "alice"}, {ID: "other", SenderUserID: "bob"}}
		db.Deliveries["job"] = &delivery{ID: "job", NotificationID: "one", SenderUserID: "alice", UserID: "bob", Status: "pending"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := a.routes()
	for _, tc := range []struct {
		method, path, cookie, origin string
		body                         any
		code                         int
	}{
		{"DELETE", "/api/me/sent-notifications/one", bob, a.cfg.origin, nil, 404},
		{"DELETE", "/api/me/sent-notifications/one", alice, "https://evil.example", nil, 403},
		{"DELETE", "/api/me/sent-notifications/one", alice, a.cfg.origin, nil, 200},
		{"POST", "/api/me/sent-notifications/cleanup", alice, a.cfg.origin, map[string]string{}, 400},
		{"POST", "/api/me/sent-notifications/cleanup", alice, a.cfg.origin, map[string]string{"confirm": "LÖSCHEN"}, 200},
	} {
		r := request(t, h, tc.method, tc.path, tc.body, tc.origin, "", tc.cookie)
		if r.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.path, r.Code, r.Body.String())
		}
	}
	snap := a.db.snapshot()
	if len(snap.Notifications["bob"]) != 3 || snap.Deliveries["job"].Status != "pending" || !snap.Deliveries["job"].SentDeleted || snap.Notifications["bob"][2].SentDeleted {
		t.Fatal("cleanup changed recipient or other sender state")
	}
	r := request(t, h, "GET", "/api/me/sent-notifications", nil, "", "", alice)
	if !strings.Contains(r.Body.String(), `"notifications":[]`) {
		t.Fatal(r.Body.String())
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.sqlDB.Close()
	if !reopened.snapshot().Deliveries["job"].SentDeleted {
		t.Fatal("deletion not persisted")
	}
}
