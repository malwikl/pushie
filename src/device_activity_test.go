package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func activityProof(key, challenge, deviceID string) string {
	secret, _ := base64.RawURLEncoding.DecodeString(key)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("pushie-device-activity-v1:" + challenge + ":" + deviceID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestDeviceActivityRequiresSessionAndSubscriptionProof(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	alice, bob := signIn(t, a, "alice"), signIn(t, a, "bob")
	if err := a.db.update(func(db *database) error {
		db.Devices["phone"] = &device{ID: "phone", UserID: "alice", Name: "Phone", Subscription: dummySubscription("https://web.push.apple.com/phone")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := a.routes()
	profile := request(t, h, "GET", "/api/me", nil, "", "", alice)
	var data struct {
		Challenge string `json:"deviceChallenge"`
	}
	if err := json.Unmarshal(profile.Body.Bytes(), &data); err != nil || data.Challenge == "" {
		t.Fatal("missing session challenge")
	}
	key := a.db.snapshot().Devices["phone"].Subscription.Keys.Auth
	proof := activityProof(key, data.Challenge, "phone")
	for _, tc := range []struct {
		cookie, id, proof, origin string
		want                      int
	}{
		{"", "phone", proof, a.cfg.origin, 401},
		{bob, "phone", proof, a.cfg.origin, 403},
		{alice, "phone", "", a.cfg.origin, 403},
		{alice, "phone", activityProof(key, "old-session", "phone"), a.cfg.origin, 403},
		{alice, "missing", proof, a.cfg.origin, 403},
		{alice, "phone", proof, "https://evil.example", 403},
	} {
		r := request(t, h, "POST", "/api/me/device-activity", map[string]string{"deviceId": tc.id, "proof": tc.proof}, tc.origin, "", tc.cookie)
		if r.Code != tc.want {
			t.Fatalf("got %d want %d: %s", r.Code, tc.want, r.Body.String())
		}
		if a.db.snapshot().Devices["phone"].LastActivityAt != nil {
			t.Fatal("unverified activity recorded")
		}
	}
	r := request(t, h, "POST", "/api/me/device-activity", map[string]string{"deviceId": "phone", "proof": proof}, a.cfg.origin, "", alice)
	if r.Code != 200 {
		t.Fatalf("verified activity: %d %s", r.Code, r.Body.String())
	}
	first := a.db.snapshot().Devices["phone"].LastActivityAt
	if first == nil || time.Since(*first) > time.Minute {
		t.Fatal("timestamp missing")
	}
	request(t, h, "POST", "/api/me/device-activity", map[string]string{"deviceId": "phone", "proof": proof}, a.cfg.origin, "", alice)
	if !a.db.snapshot().Devices["phone"].LastActivityAt.Equal(*first) {
		t.Fatal("activity writes not throttled")
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.snapshot().Devices["phone"].LastActivityAt.Equal(*first) {
		t.Fatal("timestamp not persisted")
	}
	profile = request(t, h, "GET", "/api/me", nil, "", "", alice)
	var result struct {
		Devices []struct {
			LastActivityAt *time.Time `json:"lastActivityAt"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(profile.Body.Bytes(), &result); err != nil || len(result.Devices) != 1 || result.Devices[0].LastActivityAt == nil {
		t.Fatal("timestamp absent from profile")
	}
}
