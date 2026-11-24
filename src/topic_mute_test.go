package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTopicMuteUntil(t *testing.T) {
	// The next local midnight is 22 hours away on this spring-forward day.
	now := time.Date(2026, time.March, 8, 6, 0, 0, 0, time.UTC)
	for duration, elapsed := range map[string]time.Duration{
		"15m": 15 * time.Minute, "30m": 30 * time.Minute, "60m": time.Hour,
		"4h": 4 * time.Hour, "tomorrow": 22 * time.Hour,
	} {
		got, err := topicMuteUntil(now, duration, "America/New_York")
		if err != nil || !got.Equal(now.Add(elapsed)) {
			t.Errorf("%s: got %v, %v, want %v", duration, got, err, now.Add(elapsed))
		}
	}
	if _, err := topicMuteUntil(now, "tomorrow", "Invalid/Zone"); err == nil {
		t.Fatal("accepted an invalid time zone")
	}
}

func TestTopicMuteKeepsInboxAndIsolatesAccounts(t *testing.T) {
	a := testApp(t)
	seedTopicDevices(t, a)
	if err := a.db.update(func(db *database) error {
		db.Devices["bob"].Topics = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := a.routes()
	alice, bob := signIn(t, a, "alice"), signIn(t, a, "bob")
	const path = "/api/me/topics/backup/mute"
	send := func(target, value string, queued, inbox int) string {
		t.Helper()
		r := request(t, h, "POST", "/api/send", map[string]string{target: value, "body": "Notification"}, "", a.cfg.sendToken, "")
		var result sendResult
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Queued != queued || result.InboxUsers != inbox {
			t.Fatalf("unexpected send result: %d %s", r.Code, r.Body.String())
		}
		return result.NotificationID
	}
	setMute := func(duration string) {
		t.Helper()
		r := request(t, h, "PUT", path, map[string]string{"duration": duration}, a.cfg.origin, "", alice)
		if r.Code != 200 {
			t.Fatalf("setting %s: %d %s", duration, r.Code, r.Body.String())
		}
	}
	previous := send("topic", "backup", 3, 2)
	for _, tc := range []struct {
		path, origin, cookie, duration string
		status                         int
	}{
		{path, a.cfg.origin, "", "15m", 401},
		{path, "https://other.example", alice, "15m", 403},
		{path, a.cfg.origin, alice, "15h", 400},
		{"/api/me/topics/system/mute", a.cfg.origin, bob, "15m", 404},
	} {
		r := request(t, h, "PUT", tc.path, map[string]string{"duration": tc.duration}, tc.origin, "", tc.cookie)
		if r.Code != tc.status {
			t.Fatalf("got %d want %d: %s", r.Code, tc.status, r.Body.String())
		}
	}

	setMute("15m")
	opened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	persisted := opened.snapshot()
	opened.Close()
	if !persisted.Users["alice"].topicMuted("backup", time.Now()) {
		t.Fatal("mute was not persisted")
	}
	for _, d := range deliveryViews(persisted, previous, "") {
		if (d.Status == "muted") != (d.UserID == "alice") {
			t.Fatalf("incorrect state for existing delivery: %+v", d)
		}
	}
	for _, cookie := range []string{alice, bob} {
		profile := request(t, h, "GET", "/api/me", nil, "", "", cookie)
		var view struct {
			TopicMutes map[string]time.Time `json:"topicMutes"`
		}
		if profile.Code != 200 || json.Unmarshal(profile.Body.Bytes(), &view) != nil || view.TopicMutes["backup"].After(time.Now()) != (cookie == alice) {
			t.Fatalf("incorrect profile mute state: %d %s", profile.Code, profile.Body.String())
		}
	}
	muted := send("topic", "backup", 1, 2)
	snap := a.db.snapshot()
	if got := snap.Notifications["alice"]; len(got) != 2 || got[1].ID != muted {
		t.Fatalf("muted notification missing from inbox: %+v", got)
	}
	if jobs := deliveryViews(snap, muted, ""); len(jobs) != 1 || jobs[0].UserID != "bob" {
		t.Fatalf("incorrect deliveries for muted topic: %+v", jobs)
	}
	send("topic", "system", 2, 1)
	send("userId", "alice", 4, 1)

	setMute("unmute")
	send("topic", "backup", 3, 2)
	setMute("4h")
	if err := a.db.update(func(db *database) error {
		db.Users["alice"].TopicMutes["backup"] = time.Now().Add(-time.Second)
		db.Users["bob"].TopicMutes = map[string]time.Time{"backup": time.Now().Add(time.Hour)}
		db.Users["offline"] = &user{ID: "offline", Topics: []string{"backup"}, TopicMutes: map[string]time.Time{"backup": time.Now().Add(-time.Second)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resumed := send("topic", "backup", 2, 3)
	opened, err = openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	persisted = opened.snapshot()
	opened.Close()
	for _, id := range []string{"alice", "offline"} {
		if _, exists := persisted.Users[id].TopicMutes["backup"]; exists {
			t.Errorf("expired mute still persisted for %s", id)
		}
	}
	if !persisted.Users["bob"].topicMuted("backup", time.Now()) {
		t.Error("active mute was removed")
	}
	if jobs := deliveryViews(persisted, resumed, "alice"); len(jobs) != 2 {
		t.Fatalf("push delivery did not resume: %+v", jobs)
	}
	if jobs := deliveryViews(persisted, muted, "alice"); len(jobs) != 0 {
		t.Fatalf("suppressed deliveries were replayed: %+v", jobs)
	}
}
