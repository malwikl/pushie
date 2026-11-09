package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func seedAndQueue(t *testing.T, a *application) (string, string) {
	t.Helper()
	addTestUser(t, a, "alex", "admin", []string{"backup"})
	if err := a.db.update(func(db *database) error {
		db.Devices["alex-phone"] = &device{ID: "alex-phone", UserID: "alex", Name: "iPhone", Subscription: dummySubscription("https://web.push.apple.com/Q123")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := request(t, a.routes(), "POST", "/api/send", map[string]string{"topic": "backup", "title": "Hi", "body": "Test"}, "", a.cfg.sendToken, "")
	if r.Code != 200 {
		t.Fatalf("queue: %d %s", r.Code, r.Body.String())
	}
	var result sendResult
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Queued != 1 || result.Recipients != 1 {
		t.Fatalf("not queued %+v", result)
	}
	var jobID string
	for id := range a.db.snapshot().Deliveries {
		jobID = id
	}
	return result.MessageID, jobID
}
func TestRetryThenConfirmAndPersist(t *testing.T) {
	a := testApp(t)
	messageID, jobID := seedAndQueue(t, a)
	counter := 0
	a.pushSend = func(_ context.Context, payload []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		counter++
		var msg struct {
			ID            string `json:"id"`
			DeviceID      string `json:"deviceId"`
			ReceiptSecret string `json:"receiptSecret"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil || msg.ID != messageID || msg.DeviceID != "alex-phone" || len(msg.ReceiptSecret) < 32 {
			t.Fatalf("invalid encrypted payload input: %s %v", payload, err)
		}
		code := 503
		if counter > 1 {
			code = 201
		}
		return &http.Response{StatusCode: code, Status: http.StatusText(code), Header: http.Header{}, Body: io.NopCloser(bytes.NewBuffer(nil))}, nil
	}
	now := time.Now().UTC().Add(time.Second)
	a.processDueDeliveries(context.Background(), now)
	first := a.db.snapshot().Deliveries[jobID]
	if first.Status != "retry" || first.Attempts != 1 || counter != 1 {
		t.Fatalf("retry: %+v %d", first, counter)
	}
	if !first.NextAttemptAt.After(now) {
		t.Fatal("no exponential delay")
	}
	a.processDueDeliveries(context.Background(), now.Add(time.Second))
	if counter != 1 {
		t.Fatal("retried too early")
	}
	a.processDueDeliveries(context.Background(), first.NextAttemptAt.Add(time.Millisecond))
	second := a.db.snapshot().Deliveries[jobID]
	if counter != 2 || second.Status != "accepted" || second.AcceptedAt == nil {
		t.Fatalf("provider accepted: %+v", second)
	}
	bad := request(t, a.routes(), "POST", "/api/push/ack", map[string]string{"messageId": messageID, "deviceId": "alex-phone", "receiptSecret": "wrong"}, a.cfg.origin, "", "")
	if bad.Code != 404 {
		t.Fatalf("accepted bad ack: %d", bad.Code)
	}
	ok := request(t, a.routes(), "POST", "/api/push/ack", map[string]string{"messageId": messageID, "deviceId": "alex-phone", "receiptSecret": second.ReceiptSecret}, a.cfg.origin, "", "")
	if ok.Code != 200 {
		t.Fatalf("valid ack: %d %s", ok.Code, ok.Body.String())
	}
	a.processDueDeliveries(context.Background(), now.Add(3*time.Hour))
	if counter != 2 {
		t.Fatal("retried an acknowledged message")
	}
	opened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	saved := opened.snapshot().Deliveries[jobID]
	if saved.Status != "confirmed" || saved.ConfirmedAt == nil {
		t.Fatalf("ack not durable: %+v", saved)
	}
	if r := request(t, a.routes(), "GET", "/api/send/status/"+messageID, nil, "", a.cfg.sendToken, ""); r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"confirmed"`)) || bytes.Contains(r.Body.Bytes(), []byte(second.ReceiptSecret)) {
		t.Fatalf("status exposure: %d %s", r.Code, r.Body.String())
	}
}
func TestAcceptedIsTerminal(t *testing.T) {
	a := testApp(t)
	_, id := seedAndQueue(t, a)
	now := time.Now().UTC()
	if err := a.db.update(func(db *database) error {
		d := db.Deliveries[id]
		d.AcceptedAt = &now
		d.Attempts = maxDeliveryAttempts
		d.Status = "accepted"
		d.NextAttemptAt = now.Add(-time.Second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a.processDueDeliveries(context.Background(), now.Add(time.Second))
	if s := a.db.snapshot().Deliveries[id].Status; s != "accepted" {
		t.Fatalf("wanted terminal accepted: %s", s)
	}
}
func TestOldConfirmationWaitIsRetiredOnRestart(t *testing.T) {
	a := testApp(t)
	_, id := seedAndQueue(t, a)
	now := time.Now().UTC()
	if err := a.db.update(func(db *database) error {
		d := db.Deliveries[id]
		d.AcceptedAt = &now
		d.Status = "unconfirmed"
		d.NextAttemptAt = now.Add(time.Minute)
		d.LastError = "Push service accepted notification; browser receipt missing"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	d := reopened.snapshot().Deliveries[id]
	if d.Status != "accepted" || !d.NextAttemptAt.IsZero() || d.LastError != "" || d.ConfirmedAt != nil {
		t.Fatalf("old confirmation wait not retired correctly: %+v", d)
	}
	for _, record := range reopened.snapshot().MessageLog {
		for _, view := range record.Deliveries {
			if view.DeviceID == d.DeviceID && (view.Status != "accepted" || view.NextAttemptAt != nil || view.LastError != "") {
				t.Fatalf("audit log still waiting: %+v", view)
			}
		}
	}
}

func TestInvalidSubscriptionStopsRetries(t *testing.T) {
	a := testApp(t)
	_, id := seedAndQueue(t, a)
	a.pushSend = func(_ context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		return &http.Response{StatusCode: 410, Status: "410 Gone", Header: http.Header{}, Body: io.NopCloser(strings.NewReader("expired"))}, nil
	}
	a.processDueDeliveries(context.Background(), time.Now().Add(time.Second))
	snap := a.db.snapshot()
	if snap.Deliveries[id].Status != "failed" || snap.Devices["alex-phone"] != nil {
		t.Fatalf("expired push subscription not pruned")
	}
}
func TestAckAndStatusAccessControl(t *testing.T) {
	a := testApp(t)
	id, _ := seedAndQueue(t, a)
	h := a.routes()
	if r := request(t, h, "GET", "/api/send/status/"+id, nil, "", "", ""); r.Code != 401 {
		t.Fatalf("leaked status %d", r.Code)
	}
	if r := request(t, h, "GET", "/api/admin/deliveries/"+id, nil, "", "", ""); r.Code != 401 {
		t.Fatalf("leaked admin status %d", r.Code)
	}
	cookie := signIn(t, a, "alex")
	if r := request(t, h, "GET", "/api/admin/deliveries/"+id, nil, "", "", cookie); r.Code != 200 {
		t.Fatalf("admin status %d", r.Code)
	}
	if r := request(t, h, "GET", "/api/me/messages", nil, "", "", cookie); r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"deliveries"`)) {
		t.Fatalf("inbox status %d", r.Code)
	}
}

func TestExternalIdempotencyKeyDoesNotDuplicate(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alex", "", []string{"system"})
	h := a.routes()
	payload := []byte(`{"topic":"system","title":"Hi","body":"Same"}`)
	send := func(body []byte) (int, sendResult, string) {
		r := httptest.NewRequest("POST", "/api/send", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+a.cfg.sendToken)
		r.Header.Set("Idempotency-Key", "nightly-backup-2026-11-07")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got sendResult
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		return w.Code, got, w.Header().Get("Idempotent-Replay")
	}
	code, one, _ := send(payload)
	if code != 200 {
		t.Fatalf("first: %d", code)
	}
	code, two, replay := send(payload)
	if code != 200 || one.MessageID != two.MessageID || replay != "true" {
		t.Fatalf("duplicate: %d %+v %+v", code, one, two)
	}
	if len(a.db.snapshot().Messages["alex"]) != 1 {
		t.Fatal("duplicate inbox event")
	}
	code, _, _ = send([]byte(`{"topic":"system","title":"Hi","body":"Changed"}`))
	if code != 409 {
		t.Fatalf("idempotency collision: %d", code)
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.snapshot().Idempotency) != 1 {
		t.Fatal("idempotency not persisted")
	}
}

func TestDeliveryNormalizesVAPIDSubscriber(t *testing.T) {
	for _, subject := range []string{"mailto:push@example.com", "https://example.com/contact"} {
		t.Run(subject, func(t *testing.T) {
			a := testApp(t)
			a.cfg.subject = subject
			_, jobID := seedAndQueue(t, a)
			called := false
			a.pushSend = func(_ context.Context, _ []byte, _ *webpush.Subscription, options *webpush.Options) (*http.Response, error) {
				called = true
				// The library prepends mailto: unless the subscriber is an HTTPS URL.
				actual := options.Subscriber
				if !strings.HasPrefix(actual, "https:") {
					actual = "mailto:" + actual
				}
				if actual != subject {
					t.Fatalf("JWT subject = %q, want %q", actual, subject)
				}
				return &http.Response{StatusCode: 201, Status: "201 Created", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			if err := a.processOneDelivery(context.Background(), jobID, time.Now().UTC().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("push transport was not called")
			}
		})
	}
}

func TestNotificationNativeNavigation(t *testing.T) {
	payload, err := jsonNotification(notification{ID: "a/b", Body: "Hello"}, "device", "receipt", "https://push.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var msg struct {
		WebPush       int    `json:"web_push"`
		ID            string `json:"id"`
		DeviceID      string `json:"deviceId"`
		ReceiptSecret string `json:"receiptSecret"`
		Notification  struct {
			Title    string            `json:"title"`
			Body     string            `json:"body"`
			Navigate string            `json:"navigate"`
			Tag      string            `json:"tag"`
			Data     map[string]string `json:"data"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.WebPush != 8030 || msg.Notification.Navigate != "https://push.example.com/?message=a%2Fb" || msg.Notification.Title != "Push" || msg.Notification.Body != "Hello" || msg.Notification.Tag != "a/b" || msg.Notification.Data["id"] != "a/b" {
		t.Fatalf("invalid native navigation payload: %s", payload)
	}
	if msg.ID != "a/b" || msg.DeviceID != "device" || msg.ReceiptSecret != "receipt" {
		t.Fatal("legacy worker fields must remain available during rollout")
	}
}

func TestNativeNotificationPreservesFullBodyWithinPayloadBudget(t *testing.T) {
	body := strings.Repeat("✅", 660)
	payload, err := jsonNotification(notification{ID: strings.Repeat("x", 43), Title: "Report", Body: body}, strings.Repeat("d", 43), strings.Repeat("r", 43), "https://push.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > 3000 {
		t.Fatalf("payload exceeds budget: %d", len(payload))
	}
	var msg struct {
		Body         string `json:"body"`
		Notification struct {
			Body string `json:"body"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Notification.Body != body || len(msg.Body) > 163 {
		t.Fatal("full native body or legacy preview incorrect")
	}
}

func TestAcceptedPushWithoutReceiptIsNeverResent(t *testing.T) {
	a := testApp(t)
	messageID, jobID := seedAndQueue(t, a)
	sends := 0
	a.pushSend = func(context.Context, []byte, *webpush.Subscription, *webpush.Options) (*http.Response, error) {
		sends++
		return &http.Response{StatusCode: 201, Status: "201 Created", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	now := time.Now().UTC().Add(time.Second)
	a.processDueDeliveries(context.Background(), now)
	accepted := a.db.snapshot().Deliveries[jobID]
	if sends != 1 || accepted.Status != "accepted" || !accepted.NextAttemptAt.IsZero() {
		t.Fatalf("not accepted: %+v", accepted)
	}
	// Simulate a restart with an existing accepted delivery lacking a browser ACK.
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.sqlDB.Close()
	a.db = reopened
	for _, elapsed := range []time.Duration{16 * time.Minute, time.Hour, 25 * time.Hour} {
		a.processDueDeliveries(context.Background(), now.Add(elapsed))
	}
	result := a.db.snapshot().Deliveries[jobID]
	if sends != 1 || result.Attempts != 1 || result.Status != "accepted" || !result.NextAttemptAt.IsZero() {
		t.Fatalf("resent accepted push: sends=%d %+v", sends, result)
	}
	// A delayed, valid browser receipt can still confirm the original delivery.
	r := request(t, a.routes(), "POST", "/api/push/ack", map[string]string{"messageId": messageID, "deviceId": "alex-phone", "receiptSecret": accepted.ReceiptSecret}, a.cfg.origin, "", "")
	if r.Code != 200 || a.db.snapshot().Deliveries[jobID].Status != "confirmed" {
		t.Fatalf("late receipt rejected: %d", r.Code)
	}
}
