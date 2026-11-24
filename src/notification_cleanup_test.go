package main

import (
	"encoding/json"
	"testing"
)

func TestSelectedInboxCleanupPreservesOtherNotificationsAndDeliveries(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	if err := a.db.update(func(db *database) error {
		db.Notifications["alice"] = []inboxNotification{{ID: "received", SenderUserID: "bob"}, {ID: "self", SenderUserID: "alice"}, {ID: "keep", SenderUserID: "bob"}, {ID: "arrived-later", SenderUserID: "bob"}}
		db.Notifications["bob"] = []inboxNotification{{ID: "received", SenderUserID: "bob"}, {ID: "self", SenderUserID: "alice"}, {ID: "private", SenderUserID: "bob"}}
		db.Deliveries["job"] = &delivery{ID: "job", NotificationID: "self", UserID: "bob", SenderUserID: "alice", Status: "pending"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cookie := signIn(t, a, "alice")
	h := a.routes()
	for _, tc := range []struct {
		cookie, origin string
		body           any
		code           int
	}{
		{"", a.cfg.origin, map[string]any{"confirm": "LÖSCHEN", "ids": []string{"received"}}, 401},
		{cookie, "https://evil.example", map[string]any{"confirm": "LÖSCHEN", "ids": []string{"received"}}, 403},
		{cookie, a.cfg.origin, map[string]any{"ids": []string{"received"}}, 400},
		{cookie, a.cfg.origin, map[string]any{"confirm": "LÖSCHEN"}, 400},
		{cookie, a.cfg.origin, map[string]any{"confirm": "LÖSCHEN", "ids": []string{}}, 400},
	} {
		r := request(t, h, "POST", "/api/me/notifications/cleanup", tc.body, tc.origin, "", tc.cookie)
		if r.Code != tc.code {
			t.Fatalf("got %d want %d: %s", r.Code, tc.code, r.Body.String())
		}
	}
	r := request(t, h, "POST", "/api/me/notifications/cleanup", map[string]any{"confirm": "LÖSCHEN", "ids": []string{"received", "self", "self", "private"}}, a.cfg.origin, "", cookie)
	var result struct {
		Deleted int `json:"deleted"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Deleted != 2 {
		t.Fatalf("cleanup: %d %s", r.Code, r.Body.String())
	}
	snap := a.db.snapshot()
	if len(snap.Notifications["alice"]) != 2 || snap.Notifications["alice"][0].ID != "keep" || snap.Notifications["alice"][1].ID != "arrived-later" {
		t.Fatal("deleted unseen/unselected notifications")
	}
	if len(snap.Notifications["bob"]) != 3 || snap.Notifications["bob"][0].SentDeleted || !snap.Notifications["bob"][1].SentDeleted || snap.Notifications["bob"][2].SentDeleted {
		t.Fatal("changed another account or lost self-notification history semantics")
	}
	if job := snap.Deliveries["job"]; job == nil || job.Status != "pending" || !job.SentDeleted {
		t.Fatal("disrupted active delivery or resurrected own sent history")
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.snapshot().Notifications["alice"]) != 2 {
		t.Fatal("cleanup not persisted")
	}
}

func TestSelectedSentCleanupDoesNotHideUnselectedOrOtherSenders(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	if err := a.db.update(func(db *database) error {
		db.Notifications["bob"] = []inboxNotification{{ID: "selected", SenderUserID: "alice"}, {ID: "keep", SenderUserID: "alice"}, {ID: "private", SenderUserID: "bob"}}
		db.Deliveries["job"] = &delivery{ID: "job", NotificationID: "selected", SenderUserID: "alice", UserID: "bob", Status: "pending"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cookie := signIn(t, a, "alice")
	r := request(t, a.routes(), "POST", "/api/me/sent-notifications/cleanup", map[string]any{"confirm": "LÖSCHEN", "ids": []string{}}, a.cfg.origin, "", cookie)
	if r.Code != 400 {
		t.Fatalf("empty selection accepted: %d", r.Code)
	}
	r = request(t, a.routes(), "POST", "/api/me/sent-notifications/cleanup", map[string]any{"confirm": "LÖSCHEN", "ids": []string{"selected", "private"}}, a.cfg.origin, "", cookie)
	if r.Code != 200 {
		t.Fatalf("cleanup: %d %s", r.Code, r.Body.String())
	}
	snap := a.db.snapshot()
	if len(snap.Notifications["bob"]) != 3 || !snap.Notifications["bob"][0].SentDeleted || snap.Notifications["bob"][1].SentDeleted || snap.Notifications["bob"][2].SentDeleted || snap.Deliveries["job"].Status != "pending" {
		t.Fatal("selected cleanup changed recipients or unrelated history")
	}
}
