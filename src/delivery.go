package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Web Push provider HTTP success means *accepted*, not received by a browser.
// Confirmation is a signed (unguessable token) application callback from the service worker.
const (
	maxDeliveryAttempts = 8
	deliveryLifetime    = 24 * time.Hour
	initialRetryDelay   = 30 * time.Second
	maxRetryDelay       = 30 * time.Minute
	workerInterval      = 5 * time.Second
	pushTTL             = 3600
)

type delivery struct {
	SentDeleted      bool         `json:"sentDeleted,omitempty"` // Hide sender history without removing recipient data or delivery jobs.
	SenderUserID     string       `json:"senderUserId,omitempty"`
	SubscriptionHash string       `json:"subscriptionHash,omitempty"`
	ID               string       `json:"id"`
	MessageID        string       `json:"messageId"`
	UserID           string       `json:"userId"`
	DeviceID         string       `json:"deviceId"`
	DeviceName       string       `json:"deviceName"`
	Content          notification `json:"content"`
	ReceiptSecret    string       `json:"receiptSecret"` // Not returned from any API. Store file mode is 0600.
	Status           string       `json:"status"`        // pending, sending, retry, accepted, confirmed, unconfirmed, failed
	Attempts         int          `json:"attempts"`
	NextAttemptAt    time.Time    `json:"nextAttemptAt,omitempty"`
	CreatedAt        time.Time    `json:"createdAt"`
	ExpiresAt        time.Time    `json:"expiresAt"`
	AcceptedAt       *time.Time   `json:"acceptedAt,omitempty"`
	ConfirmedAt      *time.Time   `json:"confirmedAt,omitempty"`
	LastError        string       `json:"lastError,omitempty"`
}

type deliveryView struct {
	UserID        string     `json:"userId"`
	DeviceID      string     `json:"deviceId"`
	DeviceName    string     `json:"deviceName"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
	AcceptedAt    *time.Time `json:"acceptedAt,omitempty"`
	ConfirmedAt   *time.Time `json:"confirmedAt,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
}

func viewDelivery(d *delivery) deliveryView {
	v := deliveryView{UserID: d.UserID, DeviceID: d.DeviceID, DeviceName: d.DeviceName, Status: d.Status, Attempts: d.Attempts, AcceptedAt: d.AcceptedAt, ConfirmedAt: d.ConfirmedAt, LastError: d.LastError}
	if d.Status == "pending" || d.Status == "retry" || d.Status == "sending" {
		next := d.NextAttemptAt
		if !next.IsZero() {
			v.NextAttemptAt = &next
		}
	}
	return v
}

func deliveryViews(db database, messageID, userID string) []deliveryView {
	result := make([]deliveryView, 0)
	for _, d := range db.Deliveries {
		if d.MessageID == messageID && (userID == "" || d.UserID == userID) {
			result = append(result, viewDelivery(d))
		}
	}
	// Deterministic output for clients and tests.
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].DeviceID > result[j].DeviceID {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

// Triggered by a remote script or CLI, or by an authenticated admin viewing delivery state.
func (a *application) sendStatusHandler(w http.ResponseWriter, r *http.Request) {
	r, ok := a.authorizeSend(w, r)
	if !ok {
		return
	}
	a.deliveryStatus(w, r, r.PathValue("id"))
}
func (a *application) deliveryStatus(w http.ResponseWriter, r *http.Request, id string) {
	if len(id) < 10 || len(id) > 128 {
		failure(w, 404, "Nachricht nicht gefunden")
		return
	}
	snap := a.db.snapshot()
	if p, ok := r.Context().Value(personalSenderContext{}).(personalSender); ok {
		owned := false
		for _, m := range snap.Messages[p.UserID] {
			if m.ID == id && m.SenderUserID == p.UserID {
				owned = true
			}
		}
		for _, d := range snap.Deliveries {
			if d.MessageID == id && d.UserID == p.UserID && d.SenderUserID == p.UserID {
				owned = true
			}
		}
		if !owned {
			failure(w, 404, "Nachricht nicht gefunden")
			return
		}
	}
	views := deliveryViews(snap, id, "")
	count := 0
	for _, messages := range snap.Messages {
		for _, m := range messages {
			if m.ID == id {
				count++
			}
		}
	}
	if count == 0 && len(views) == 0 {
		failure(w, 404, "Nachricht nicht gefunden")
		return
	}
	counts := map[string]int{}
	for _, v := range views {
		counts[v.Status]++
	}
	reply(w, 200, map[string]any{"messageId": id, "devices": views, "states": counts, "inboxUsers": count})
}

func (a *application) acknowledgeDelivery(w http.ResponseWriter, r *http.Request) {
	if !a.rateLimit(w, r) {
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != a.cfg.origin {
		failure(w, 403, "Origin nicht erlaubt")
		return
	}
	var req struct {
		MessageID     string `json:"messageId"`
		DeviceID      string `json:"deviceId"`
		ReceiptSecret string `json:"receiptSecret"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.MessageID) > 128 || len(req.DeviceID) > 128 || len(req.ReceiptSecret) > 128 || req.MessageID == "" || req.DeviceID == "" || req.ReceiptSecret == "" {
		failure(w, 400, "Ungültige Empfangsbestätigung")
		return
	}
	// Reject guessed receipts using an indexed lookup before cloning/mutating
	// the store. This endpoint is intentionally accessible without a session.
	var raw []byte
	err := a.db.sqlDB.QueryRowContext(r.Context(), `SELECT payload FROM deliveries WHERE message_id=? AND device_id=? LIMIT 1`, req.MessageID, req.DeviceID).Scan(&raw)
	var candidate delivery
	if err != nil || json.Unmarshal(raw, &candidate) != nil || subtle.ConstantTimeCompare([]byte(candidate.ReceiptSecret), []byte(req.ReceiptSecret)) != 1 {
		failure(w, 404, "Bestätigung nicht gefunden oder abgelaufen")
		return
	}
	err = a.db.update(func(db *database) error {
		d := db.Deliveries[candidate.ID]
		if d == nil || subtle.ConstantTimeCompare([]byte(d.ReceiptSecret), []byte(req.ReceiptSecret)) != 1 {
			return errors.New("not found")
		}
		if d.ConfirmedAt == nil {
			now := time.Now().UTC()
			d.Status = "confirmed"
			d.ConfirmedAt = &now
			d.NextAttemptAt = time.Time{}
			d.LastError = ""
		}
		return nil
	})
	if err != nil {
		failure(w, 404, "Bestätigung nicht gefunden oder abgelaufen")
		return
	}
	reply(w, 200, map[string]bool{"confirmed": true})
}

func retryDelay(attempt int) time.Duration {
	d := initialRetryDelay
	for i := 1; i < attempt; i++ {
		if d >= maxRetryDelay/2 {
			return maxRetryDelay
		}
		d *= 2
	}
	if d > maxRetryDelay {
		return maxRetryDelay
	}
	return d
}
func retryAfter(header string, now time.Time) time.Duration {
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

func (a *application) startDeliveryWorker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	// The JSON store is single-process only. One worker is started in main().
	go func() {
		defer close(done)
		tick := time.NewTicker(workerInterval)
		defer tick.Stop()
		a.processDueDeliveries(ctx, time.Now().UTC())
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				a.processDueDeliveries(ctx, now.UTC())
			}
		}
	}()
	return done
}

func (a *application) processDueDeliveries(ctx context.Context, now time.Time) {
	// Query the indexed queue rather than cloning every user's inbox and secrets.
	rows, err := a.db.sqlDB.QueryContext(ctx, `SELECT id FROM deliveries WHERE status IN ('pending','retry','sending') AND (next_attempt_at IS NULL OR next_attempt_at <= ?) ORDER BY next_attempt_at,id LIMIT 100`, sqliteTime(now))
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("delivery queue query: %v", err)
		}
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		log.Printf("delivery queue read: %v", err)
		return
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := a.processOneDelivery(ctx, id, now); err != nil {
					log.Printf("delivery worker job=%s: %v", id, err)
				}
			}
		}()
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func subscriptionHash(sub webpush.Subscription) string {
	payload, _ := json.Marshal(sub)
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func (a *application) processOneDelivery(ctx context.Context, id string, now time.Time) error {
	var job delivery
	var dev device
	shouldSend := false
	err := a.db.update(func(db *database) error {
		d := db.Deliveries[id]
		if d == nil || d.Status == "confirmed" || d.Status == "accepted" || d.Status == "failed" || d.Status == "unconfirmed" || d.NextAttemptAt.After(now) {
			return nil
		}
		// A missing browser receipt must never cause another visible notification.
		// This also retires accepted jobs persisted by older server versions.
		if d.AcceptedAt != nil {
			finishDelivery(d, "")
			return nil
		}
		// On crash while sending, "sending" is recovered after its lease expires.
		if !now.Before(d.ExpiresAt) || d.Attempts >= maxDeliveryAttempts {
			finishDelivery(d, "delivery retry limit or time window exceeded")
			return nil
		}
		current := db.Devices[d.DeviceID]
		if current == nil || current.UserID != d.UserID || !validPushEndpoint(current.Subscription.Endpoint) {
			d.Status = "failed"
			d.LastError = "Push subscription removed or invalid"
			d.NextAttemptAt = time.Time{}
			return nil
		}
		job = *d
		dev = *current
		d.SubscriptionHash = subscriptionHash(current.Subscription)
		d.Status = "sending"
		d.Attempts++
		d.NextAttemptAt = now.Add(2 * time.Minute) // durable in-flight lease
		shouldSend = true
		return nil
	})
	if err != nil || !shouldSend {
		return err
	}
	job.Attempts++
	payload, err := jsonNotification(job.Content, job.DeviceID, job.ReceiptSecret, a.cfg.origin)
	if err != nil {
		return a.resolveDelivery(id, now, 0, err.Error(), 0)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	transport := a.pushSend
	if transport == nil {
		transport = webpush.SendNotificationWithContext
	}
	resp, sendErr := transport(ctx, payload, &dev.Subscription, &webpush.Options{
		// webpush-go v1.4.0 adds mailto: to non-HTTPS subscribers.
		Subscriber: strings.TrimPrefix(a.cfg.subject, "mailto:"), VAPIDPublicKey: a.cfg.publicKey, VAPIDPrivateKey: a.cfg.privateKey,
		TTL: pushTTL, HTTPClient: client,
	})
	if sendErr != nil {
		detail := sendErr.Error()
		var endpointErr *url.Error
		if errors.As(sendErr, &endpointErr) {
			detail = endpointErr.Err.Error()
		}
		return a.resolveDelivery(id, now, 0, detail, 0)
	}
	if resp == nil {
		return a.resolveDelivery(id, now, 0, "empty response", 0)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	detail := resp.Status
	var provider struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(body, &provider) == nil && providerReason.MatchString(provider.Reason) {
		detail = provider.Reason
	}
	_ = resp.Body.Close()
	return a.resolveDelivery(id, now, resp.StatusCode, detail, retryAfter(resp.Header.Get("Retry-After"), now))
}

var providerReason = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_. -]{0,119}$`)

func finishDelivery(d *delivery, reason string) {
	// A provider has possibly queued an accepted notification; lack of a
	// callback cannot be interpreted as delivery failure.
	if d.AcceptedAt != nil {
		d.Status = "accepted"
		reason = ""
	} else {
		d.Status = "failed"
	}
	d.LastError = reason
	d.NextAttemptAt = time.Time{}
}

func (a *application) resolveDelivery(id string, now time.Time, code int, detail string, suggestedDelay time.Duration) error {
	slog.Info("push.provider_response", "delivery_id", id, "http_status", code, "retry_after_ms", suggestedDelay.Milliseconds())
	return a.db.update(func(db *database) error {
		d := db.Deliveries[id]
		if d == nil || d.Status == "confirmed" {
			return nil
		} // SW ACK may have arrived while sending.
		if dev := db.Devices[d.DeviceID]; dev != nil && d.SubscriptionHash != "" && d.SubscriptionHash != subscriptionHash(dev.Subscription) {
			d.Status = "retry"
			d.LastError = "Subscription changed during send"
			d.NextAttemptAt = now.Add(initialRetryDelay)
			return nil
		}
		switch {
		case code >= 200 && code < 300:
			d.Status = "accepted"
			d.LastError = ""
			accepted := now
			d.AcceptedAt = &accepted
			d.NextAttemptAt = time.Time{}
		case code == 404 || code == 410:
			d.Status = "failed"
			d.LastError = fmt.Sprintf("Subscription invalid (HTTP %d)", code)
			d.NextAttemptAt = time.Time{}
			// Remove only when this is still the same subscription; new registrations must not be removed.
			delete(db.Devices, d.DeviceID)
		case code == 408 || code == 425 || code == 429 || code >= 500 || code == 0:
			d.Status = "retry"
			if len(detail) > 180 {
				detail = detail[:180]
			}
			d.LastError = detail
			delay := retryDelay(d.Attempts)
			if suggestedDelay > delay {
				delay = suggestedDelay
			}
			if delay > deliveryLifetime {
				delay = deliveryLifetime
			}
			d.NextAttemptAt = now.Add(delay)
		default:
			d.Status = "failed"
			d.LastError = fmt.Sprintf("Permanent provider error HTTP %d", code)
			if providerReason.MatchString(detail) && detail != http.StatusText(code) {
				d.LastError += ": " + detail
			}
			d.NextAttemptAt = time.Time{}
		}
		if d.Status == "retry" {
			if d.Attempts >= maxDeliveryAttempts || !d.NextAttemptAt.Before(d.ExpiresAt) {
				finishDelivery(d, "delivery retry limit or time window exceeded")
			}
		}
		return nil
	})
}

func jsonNotification(n notification, deviceID, receiptSecret, origin string) ([]byte, error) {
	// Keep legacy fields during the service-worker rollout. Safari can use the
	// declarative URL even when no notificationclick event reaches JavaScript.
	type declarativeNotification struct {
		Title    string            `json:"title"`
		Body     string            `json:"body"`
		Navigate string            `json:"navigate"`
		Tag      string            `json:"tag,omitempty"`
		Data     map[string]string `json:"data"`
	}
	type message struct {
		notification
		DeviceID      string                  `json:"deviceId"`
		ReceiptSecret string                  `json:"receiptSecret"`
		WebPush       int                     `json:"web_push"`
		Declarative   declarativeNotification `json:"notification"`
	}
	title := n.Title
	if title == "" {
		title = "Push"
	}
	target := strings.TrimRight(origin, "/") + "/?message=" + url.QueryEscape(n.ID)
	// Older workers still receive a short preview; avoid duplicating the full
	// body and exceeding APNs' encrypted payload limit during rollout.
	legacy := n
	if len(legacy.Body) > 160 {
		legacy.Body = legacy.Body[:160]
		for !utf8.ValidString(legacy.Body) {
			legacy.Body = legacy.Body[:len(legacy.Body)-1]
		}
		legacy.Body += "…"
	}
	return json.Marshal(message{notification: legacy, DeviceID: deviceID, ReceiptSecret: receiptSecret,
		WebPush: 8030, Declarative: declarativeNotification{Title: title, Body: n.Body,
			Navigate: target, Tag: n.ID, Data: map[string]string{"id": n.ID}}})
}
