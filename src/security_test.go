package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func TestTrustedProxyClientIP(t *testing.T) {
	a := testApp(t)
	a.cfg.trustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, tc := range []struct{ peer, forwarded, want string }{
		{"192.0.2.1:123", "203.0.113.1", "192.0.2.1"},
		{"10.0.0.1:123", "203.0.113.1", "203.0.113.1"},
		{"10.0.0.1:123", "198.51.100.1, 203.0.113.1, 10.0.0.2", "203.0.113.1"},
		{"10.0.0.1:123", "bad", "10.0.0.1"},
	} {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		if got := a.clientIP(r); got != tc.want {
			t.Errorf("%+v: got %s", tc, got)
		}
	}
}
func TestRateLimitBudgetsAreSeparate(t *testing.T) {
	a := testApp(t)
	check := func(path string) bool {
		return a.rateLimit(httptest.NewRecorder(), httptest.NewRequest("POST", path, nil))
	}
	for i := 0; i < 30; i++ {
		if !check("/api/auth/login/begin") {
			t.Fatal("early limit")
		}
	}
	if check("/api/auth/login/begin") {
		t.Fatal("auth limit not enforced")
	}
	if !check("/api/push/ack") || !check("/api/send") {
		t.Fatal("auth flood blocked unrelated buckets")
	}
}
func TestDeviceQuotaAndRefresh(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	cookie := signIn(t, a, "alice")
	h := a.routes()
	for i := 0; i <= maxDevicesPerUser; i++ {
		r := request(t, h, "POST", "/api/me/devices", map[string]any{"name": "Device", "subscription": dummySubscription(fmt.Sprintf("https://web.push.apple.com/%d", i))}, a.cfg.origin, "", cookie)
		want := 201
		if i == maxDevicesPerUser {
			want = 409
		}
		if r.Code != want {
			t.Fatalf("device %d: %d %s", i, r.Code, r.Body.String())
		}
	}
	r := request(t, h, "POST", "/api/me/devices", map[string]any{"name": "Updated", "subscription": dummySubscription("https://web.push.apple.com/0")}, a.cfg.origin, "", cookie)
	if r.Code != 201 {
		t.Fatal("quota blocked refresh")
	}
	if len(a.db.snapshot().Devices) != maxDevicesPerUser {
		t.Fatal("quota exceeded")
	}
}
func TestOldProviderResponsePreservesRefreshedSubscription(t *testing.T) {
	a := testApp(t)
	_, id := seedAndQueue(t, a)
	a.pushSend = func(_ context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		if err := a.db.update(func(db *database) error {
			db.Devices["alex-phone"].Subscription.Keys.Auth = strings.Repeat("a", 22)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 410, Status: "410 Gone", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	if err := a.processOneDelivery(context.Background(), id, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	snap := a.db.snapshot()
	if snap.Devices["alex-phone"] == nil || snap.Deliveries[id].Status != "retry" {
		t.Fatal("stale response destroyed refreshed subscription")
	}
}
func TestStoreLockExcludesSecondServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	release, err := lockStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := lockStore(path); err == nil {
		again()
		release()
		t.Fatal("second server acquired lock")
	}
	release()
	again, err := lockStore(path)
	if err != nil {
		t.Fatal(err)
	}
	again()
}
func TestSelectedDevicesAndBroadcast(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	addTestUser(t, a, "bob", "", nil)
	if err := a.db.update(func(db *database) error {
		for _, id := range []string{"one", "two"} {
			db.Devices[id] = &device{ID: id, UserID: "alice", Subscription: dummySubscription("https://web.push.apple.com/" + id)}
		}
		db.Devices["other"] = &device{ID: "other", UserID: "bob", Subscription: dummySubscription("https://web.push.apple.com/other")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	send := func(body map[string]any) sendResult {
		r := request(t, a.routes(), "POST", "/api/send", body, "", a.cfg.sendToken, "")
		if r.Code != 200 {
			t.Fatalf("send: %d %s", r.Code, r.Body.String())
		}
		var result sendResult
		json.Unmarshal(r.Body.Bytes(), &result)
		return result
	}
	selected := send(map[string]any{"userId": "alice", "deviceIds": []string{"one", "one"}, "body": "hello"})
	if selected.Queued != 1 || selected.InboxUsers != 1 {
		t.Fatalf("selection: %+v", selected)
	}
	for _, body := range []map[string]any{
		{"userId": "alice", "deviceIds": []string{"other"}, "body": "bad"},
		{"userId": "alice", "deviceIds": []string{}, "body": "bad"},
		{"broadcast": true, "userId": "alice", "body": "bad"},
	} {
		r := request(t, a.routes(), "POST", "/api/send", body, "", a.cfg.sendToken, "")
		if r.Code != 400 && r.Code != 404 {
			t.Fatalf("invalid selection accepted: %d", r.Code)
		}
	}
	addTestUser(t, a, "no-device", "", nil)
	broadcast := send(map[string]any{"broadcast": true, "body": "everyone"})
	if broadcast.Queued != 3 || broadcast.InboxUsers != 3 {
		t.Fatalf("broadcast: %+v", broadcast)
	}
}
func TestQueueQuotaRejectsAtomically(t *testing.T) {
	a := testApp(t)
	_, _ = seedAndQueue(t, a)
	if err := a.db.update(func(db *database) error {
		for i := 0; i < maxPendingPerUser; i++ {
			id := fmt.Sprint(i)
			db.Deliveries[id] = &delivery{ID: id, UserID: "alex", Status: "pending"}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := len(a.db.snapshot().Messages["alex"])
	r := request(t, a.routes(), "POST", "/api/send", map[string]any{"userId": "alex", "body": "overflow"}, "", a.cfg.sendToken, "")
	if r.Code != 429 || len(a.db.snapshot().Messages["alex"]) != before {
		t.Fatalf("queue rejection not atomic: %d", r.Code)
	}
}
func TestDeliveryConcurrencyIsBounded(t *testing.T) {
	a := testApp(t)
	_, _ = seedAndQueue(t, a)
	if err := a.db.update(func(db *database) error {
		for i := 0; i < 9; i++ {
			id := fmt.Sprint(i)
			for _, job := range db.Deliveries {
				copy := *job
				copy.ID = id
				db.Deliveries[id] = &copy
				break
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var running, maximum atomic.Int32
	started := make(chan struct{}, 20)
	release := make(chan struct{})
	a.pushSend = func(_ context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			old := maximum.Load()
			if old >= n || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		<-release
		return &http.Response{StatusCode: 201, Status: "201 Created", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.processDueDeliveries(context.Background(), time.Now().UTC().Add(time.Second))
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			close(release)
			<-done
			t.Fatal("four workers did not start")
		}
	}
	close(release)
	<-done
	if maximum.Load() != 4 {
		t.Fatalf("concurrency = %d", maximum.Load())
	}
}

func TestGlobalQueueQuota(t *testing.T) {
	a := testApp(t)
	_, _ = seedAndQueue(t, a)
	if err := a.db.update(func(db *database) error {
		for i := 0; i < maxPendingDeliveries; i++ {
			id := fmt.Sprint(i)
			db.Deliveries[id] = &delivery{ID: id, UserID: "other-" + id, Status: "pending"}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := len(a.db.snapshot().Messages["alex"])
	r := request(t, a.routes(), "POST", "/api/send", map[string]any{"userId": "alex", "body": "overflow"}, "", a.cfg.sendToken, "")
	if r.Code != 429 || len(a.db.snapshot().Messages["alex"]) != before {
		t.Fatalf("global queue quota not atomic: %d", r.Code)
	}
}

func TestDeliveryWorkerStopsOnCancellation(t *testing.T) {
	a := testApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := a.startDeliveryWorker(ctx)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker ignored cancellation")
	}
}

func TestUserSnapshotsExcludeOtherAccountsAndSecrets(t *testing.T) {
	a := testApp(t)
	_, _ = seedAndQueue(t, a)
	addTestUser(t, a, "bob", "", nil)
	if err := a.db.update(func(db *database) error {
		db.Messages["bob"] = []inboxMessage{{ID: "private", Body: "private"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	inbox := a.db.userSnapshot("alex", true)
	if len(inbox.Users) != 0 || len(inbox.Devices) != 0 || len(inbox.Messages["bob"]) != 0 {
		t.Fatal("unrelated account data exposed in user snapshot")
	}
	for _, d := range inbox.Deliveries {
		if d.ReceiptSecret != "" {
			t.Fatal("receipt secret copied into user snapshot")
		}
	}
	account := a.db.userSnapshot("alex", false)
	for _, d := range account.Devices {
		if d.Subscription.Endpoint != "" {
			t.Fatal("subscription endpoint copied into account view")
		}
	}
}

func TestAuthenticatedReadTrafficDoesNotExhaustWrites(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	cookie := signIn(t, a, "alice")
	for i := 0; i < 130; i++ {
		r := httptest.NewRequest("GET", "/api/me/messages", nil)
		r.Header.Set("Cookie", cookie)
		if !a.rateLimit(httptest.NewRecorder(), r) {
			t.Fatalf("read %d was blocked", i)
		}
	}
	r := httptest.NewRequest("POST", "/api/admin/send", nil)
	r.Header.Set("Cookie", cookie)
	if !a.rateLimit(httptest.NewRecorder(), r) {
		t.Fatal("reads consumed write quota")
	}
}
