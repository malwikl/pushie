package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestReadingAndOnboardingPreferences(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	cookie := signIn(t, a, "alice")
	h := a.routes()
	for _, tc := range []struct {
		body           any
		origin, cookie string
		code           int
	}{
		{map[string]bool{"manualRead": true}, a.cfg.origin, "", 401},
		{map[string]bool{"onboardingComplete": true}, "https://other.example", cookie, 403},
		{map[string]string{"manualRead": "yes"}, a.cfg.origin, cookie, 400},
		{map[string]string{"onboardingComplete": "yes"}, a.cfg.origin, cookie, 400},
		{map[string]bool{"manualRead": true, "onboardingComplete": true}, a.cfg.origin, cookie, 200},
	} {
		r := request(t, h, "PUT", "/api/me/settings", tc.body, tc.origin, "", tc.cookie)
		if r.Code != tc.code {
			t.Fatalf("settings: got %d want %d: %s", r.Code, tc.code, r.Body.String())
		}
	}
	profile := request(t, h, "GET", "/api/me", nil, "", "", cookie)
	if !bytes.Contains(profile.Body.Bytes(), []byte(`"manualRead":true`)) || !bytes.Contains(profile.Body.Bytes(), []byte(`"onboardingComplete":true`)) {
		t.Fatalf("profile omitted preferences: %s", profile.Body.String())
	}
	request(t, h, "PUT", "/api/me/settings", map[string]string{"language": "en", "theme": "dark"}, a.cfg.origin, "", cookie)
	r := request(t, h, "PUT", "/api/me/settings", map[string]bool{"manualRead": false}, a.cfg.origin, "", cookie)
	if r.Code != 200 {
		t.Fatalf("disable manual reading: %d", r.Code)
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved := reopened.userByID("alice")
	if saved.ManualRead || !saved.OnboardingComplete || saved.Language != "en" || saved.Theme != "dark" {
		t.Fatalf("preferences not preserved: %+v", saved)
	}
	other := a.db.userByID("bob")
	if other.ManualRead || other.OnboardingComplete {
		t.Fatal("changed another account's settings")
	}
}

func TestBroadcastReasonSurvivesSentHistoryReconstruction(t *testing.T) {
	a := testApp(t)
	seedTopicDevices(t, a)
	cookie := signIn(t, a, "alice")
	r := request(t, a.routes(), "POST", "/api/admin/send", map[string]any{"broadcast": true, "body": "Broadcast"}, a.cfg.origin, "", cookie)
	var result sendResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil {
		t.Fatalf("send: %d %s", r.Code, r.Body.String())
	}
	for _, notifications := range a.db.snapshot().Notifications {
		for _, m := range notifications {
			if m.ID == result.NotificationID && m.DeliveryReason != "broadcast" {
				t.Fatalf("missing reason: %+v", m)
			}
		}
	}
	if err := a.db.update(func(db *database) error {
		for uid := range db.Notifications {
			db.Notifications[uid] = nil
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r = request(t, a.routes(), "GET", "/api/me/sent-notifications", nil, "", "", cookie)
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"deliveryReason":"broadcast"`)) {
		t.Fatalf("lost broadcast reason: %d %s", r.Code, r.Body.String())
	}
}

func TestReadAllSeparatesBroadcastAndDirect(t *testing.T) {
	for _, reason := range []string{"direct", "broadcast"} {
		t.Run(reason, func(t *testing.T) {
			a := testApp(t)
			addTestUser(t, a, "alice", "", nil)
			if err := a.db.update(func(db *database) error {
				db.Notifications["alice"] = []inboxNotification{{ID: "direct"}, {ID: "broadcast", DeliveryReason: "broadcast"}, {ID: "topic", Topic: "backup"}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			cookie := signIn(t, a, "alice")
			r := request(t, a.routes(), "POST", "/api/me/notifications/read-all", map[string]string{"deliveryReason": reason}, a.cfg.origin, "", cookie)
			if r.Code != 200 {
				t.Fatalf("mark read: %d %s", r.Code, r.Body.String())
			}
			for _, m := range a.db.snapshot().Notifications["alice"] {
				if (m.ReadAt != nil) != (m.ID == reason) {
					t.Fatalf("wrong read state: %+v", m)
				}
			}
			r = request(t, a.routes(), "POST", "/api/me/notifications/read-all", map[string]string{"deliveryReason": "unknown"}, a.cfg.origin, "", cookie)
			if r.Code != 400 {
				t.Fatalf("invalid reason accepted: %d", r.Code)
			}
		})
	}
}

func TestReadAllMarksOnlySelectedNotificationsInCurrentInbox(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	if err := a.db.update(func(db *database) error {
		db.Notifications["alice"] = []inboxNotification{{ID: "shown", Topic: "backup"}, {ID: "arrived-later", Topic: "backup"}, {ID: "hidden", Topic: "system"}}
		db.Notifications["bob"] = []inboxNotification{{ID: "shown", Topic: "backup"}, {ID: "other-account"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := a.routes()
	cookie := signIn(t, a, "alice")
	const path = "/api/me/notifications/read-all"
	for _, body := range []map[string]any{
		{"ids": nil},
		{"ids": []string{}},
		{"ids": []string{"shown"}, "topic": "backup"},
	} {
		if r := request(t, h, "POST", path, body, a.cfg.origin, "", cookie); r.Code != 400 {
			t.Fatalf("invalid selection accepted: %d %s", r.Code, r.Body.String())
		}
	}
	r := request(t, h, "POST", path, map[string]any{"ids": []string{"shown", "other-account"}}, a.cfg.origin, "", cookie)
	if r.Code != 200 {
		t.Fatalf("mark selected read: %d %s", r.Code, r.Body.String())
	}
	for uid, notifications := range a.db.snapshot().Notifications {
		for _, n := range notifications {
			wantRead := uid == "alice" && n.ID == "shown"
			if (n.ReadAt != nil) != wantRead {
				t.Errorf("wrong read state for %s/%s: %v", uid, n.ID, n.ReadAt)
			}
		}
	}
}
