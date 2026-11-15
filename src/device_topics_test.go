package main

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func seedTopicDevices(t *testing.T, a *application) {
	t.Helper()
	addTestUser(t, a, "alice", "admin", []string{"backup", "system"})
	addTestUser(t, a, "bob", "", []string{"backup"})
	if err := a.db.update(func(db *database) error {
		for id, topics := range map[string][]string{"all": nil, "backup": {"backup"}, "none": {}, "system": {"system"}} {
			db.Devices[id] = &device{ID: id, UserID: "alice", Name: id, Topics: topics, Subscription: dummySubscription("https://web.push.apple.com/" + id)}
		}
		db.Devices["bob"] = &device{ID: "bob", UserID: "bob", Name: "Bob", Topics: []string{"system"}, Subscription: dummySubscription("https://web.push.apple.com/bob")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceTopicsRouteOnlyTopicPushes(t *testing.T) {
	a := testApp(t)
	seedTopicDevices(t, a)
	for _, tc := range []struct {
		name   string
		target map[string]any
		queued int
		inbox  int
	}{
		{"backup", map[string]any{"topic": "backup"}, 2, 2},
		{"system", map[string]any{"topic": "system"}, 2, 1},
		{"account", map[string]any{"userId": "alice"}, 4, 1},
		{"device", map[string]any{"deviceId": "none"}, 1, 1},
		{"selected", map[string]any{"userId": "alice", "deviceIds": []string{"none"}}, 1, 1},
		{"broadcast", map[string]any{"broadcast": true}, 5, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.target["body"] = "Notification"
			r := request(t, a.routes(), "POST", "/api/send", tc.target, "", a.cfg.sendToken, "")
			var result sendResult
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Queued != tc.queued || result.InboxUsers != tc.inbox {
				t.Fatalf("unexpected send result: %d %s", r.Code, r.Body.String())
			}
			if tc.name == "backup" || tc.name == "system" {
				for _, job := range a.db.snapshot().Deliveries {
					if job.MessageID == result.MessageID && job.DeviceID != "all" && job.DeviceID != tc.name {
						t.Fatalf("topic sent to excluded device: %+v", job)
					}
				}
			}
		})
	}
	if got := a.db.devicesFor("backup", "", ""); len(got) != 2 {
		t.Fatalf("topic helper differs from dispatch: %+v", got)
	}
}

func TestDeviceTopicSettingsAuthorizationValidationAndPersistence(t *testing.T) {
	a := testApp(t)
	seedTopicDevices(t, a)
	owner, other := signIn(t, a, "alice"), signIn(t, a, "bob")
	h := a.routes()
	for _, tc := range []struct {
		name, path, origin, cookie string
		body                       any
		code                       int
	}{
		{"signed out", "all", a.cfg.origin, "", map[string]any{"topics": nil}, 401},
		{"other account", "all", a.cfg.origin, other, map[string]any{"topics": nil}, 404},
		{"cross origin", "all", "https://evil.example", owner, map[string]any{"topics": nil}, 403},
		{"missing device", "missing", a.cfg.origin, owner, map[string]any{"topics": nil}, 404},
		{"missing field", "all", a.cfg.origin, owner, map[string]any{}, 400},
		{"wrong type", "all", a.cfg.origin, owner, map[string]any{"topics": "backup"}, 400},
		{"missing topic", "all", a.cfg.origin, owner, map[string]any{"topics": []string{"missing"}}, 400},
		{"invalid topic", "all", a.cfg.origin, owner, map[string]any{"topics": []string{"../backup"}}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request(t, h, "PUT", "/api/me/devices/"+tc.path+"/topics", tc.body, tc.origin, "", tc.cookie)
			if r.Code != tc.code {
				t.Fatalf("got %d want %d: %s", r.Code, tc.code, r.Body.String())
			}
		})
	}
	if a.db.snapshot().Devices["all"].Topics != nil {
		t.Fatal("failed updates changed settings")
	}
	for _, selection := range [][]string{{"system", "backup", "backup"}, {}, nil} {
		r := request(t, h, "PUT", "/api/me/devices/all/topics", map[string]any{"topics": selection}, a.cfg.origin, "", owner)
		if r.Code != 200 {
			t.Fatalf("save: %d %s", r.Code, r.Body.String())
		}
		want := selection
		if len(selection) > 0 {
			want = []string{"backup", "system"}
		}
		opened, err := openStore(a.db.path)
		if err != nil {
			t.Fatal(err)
		}
		got := opened.snapshot().Devices["all"].Topics
		opened.Close()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("settings not durable (nil and empty must differ): got %#v want %#v", got, want)
		}
		profile := request(t, h, "GET", "/api/me", nil, "", "", owner)
		var view struct {
			Devices []struct {
				ID     string   `json:"id"`
				Topics []string `json:"topics"`
			} `json:"devices"`
		}
		if profile.Code != 200 || json.Unmarshal(profile.Body.Bytes(), &view) != nil {
			t.Fatalf("profile: %d %s", profile.Code, profile.Body.String())
		}
		for _, dev := range view.Devices {
			if dev.ID == "bob" || (dev.ID == "all" && !reflect.DeepEqual(dev.Topics, want)) {
				t.Fatalf("incorrect profile device: %+v", dev)
			}
		}
	}
	// Refreshing the browser's push subscription must preserve its topic selection.
	r := request(t, h, "POST", "/api/me/devices", map[string]any{"name": "Renamed", "subscription": dummySubscription("https://web.push.apple.com/none")}, a.cfg.origin, "", owner)
	if r.Code != 201 || a.db.snapshot().Devices["none"].Topics == nil {
		t.Fatalf("re-registration lost the opt-out: %d %s", r.Code, r.Body.String())
	}
	r = request(t, h, "DELETE", "/api/admin/topics/backup/web", nil, a.cfg.origin, "", owner)
	snap := a.db.snapshot()
	if r.Code != 200 || snap.Devices["backup"].Topics == nil || len(snap.Devices["backup"].Topics) != 0 || snap.Devices["all"].Topics != nil {
		t.Fatalf("deletion must remove device selections without enabling all topics: %d", r.Code)
	}
}

func TestTopicOptOutKeepsInboxAndDoesNotConsumeQueueCapacity(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", []string{"backup"})
	if err := a.db.update(func(db *database) error {
		db.Devices["none"] = &device{ID: "none", UserID: "alice", Topics: []string{}}
		for i := 0; i < maxPendingPerUser; i++ {
			id := time.Unix(int64(i), 0).String()
			db.Deliveries[id] = &delivery{ID: id, UserID: "alice", Status: "pending"}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := request(t, a.routes(), "POST", "/api/send", map[string]string{"topic": "backup", "body": "Inbox only"}, "", a.cfg.sendToken, "")
	var result sendResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Queued != 0 || result.InboxUsers != 1 {
		t.Fatalf("opted-out device consumed queue capacity or lost inbox: %d %s", r.Code, r.Body.String())
	}
}

func TestMarkAllReadRespectsTopicFilter(t *testing.T) {
	for _, tc := range []struct {
		name string
		body any
		want []bool
	}{
		{"topic", map[string]string{"topic": "backup"}, []bool{true, false, false}},
		{"direct", map[string]string{"topic": ""}, []bool{false, false, true}},
		{"all", nil, []bool{true, true, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			addTestUser(t, a, "alice", "", nil)
			addTestUser(t, a, "other", "", nil)
			if err := a.db.update(func(db *database) error {
				db.Messages["alice"] = []inboxMessage{{ID: "backup", Topic: "backup"}, {ID: "system", Topic: "system"}, {ID: "direct"}}
				db.Messages["other"] = []inboxMessage{{ID: "private", Topic: "backup"}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			r := request(t, a.routes(), "POST", "/api/me/messages/read-all", tc.body, a.cfg.origin, "", signIn(t, a, "alice"))
			if r.Code != 200 {
				t.Fatalf("mark read: %d %s", r.Code, r.Body.String())
			}
			snap := a.db.snapshot()
			for i, message := range snap.Messages["alice"] {
				if (message.ReadAt != nil) != tc.want[i] {
					t.Fatalf("wrong read state for %s", message.ID)
				}
			}
			if snap.Messages["other"][0].ReadAt != nil {
				t.Fatal("marked another account's message read")
			}
		})
	}
}
