package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const maxInboxMessages = 200
const maxDevicesPerUser = 20
const maxPendingDeliveries = 10000
const maxPendingPerUser = 1000

var idempotencyKeyRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{12,128}$`)

type webSenderContext struct{}

type notification struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	LongText string `json:"longText,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Topic    string `json:"topic,omitempty"`
	ID       string `json:"id,omitempty"`
}
type sendRequest struct {
	notification
	DeviceIDs []string `json:"deviceIds,omitempty"`
	Broadcast bool     `json:"broadcast,omitempty"`
	UserID    string   `json:"userId,omitempty"`
	DeviceID  string   `json:"deviceId,omitempty"`
}

type sendResult struct {
	// Legacy fields remain for backwards-compatible CLI consumers. With the
	// durable queue, "accepted" is initially zero, not a delivery guarantee.
	Accepted   int    `json:"accepted"`
	Failed     int    `json:"failed"`
	Expired    int    `json:"expired"`
	Recipients int    `json:"recipients"`
	Queued     int    `json:"queued"`
	InboxUsers int    `json:"inboxUsers"`
	MessageID  string `json:"messageId"`
	StatusURL  string `json:"statusUrl"`
}

func (a *application) sendHandler(w http.ResponseWriter, r *http.Request) {
	r, ok := a.authorizeSend(w, r)
	if !ok {
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != a.cfg.origin {
		failure(w, 403, "Origin nicht erlaubt")
		return
	}
	a.dispatchFromBody(w, r)
}

// All targets are resolved AT ACCEPTANCE TIME, ensuring that retries send
// only to the original subscribed users and devices, never new subscribers.
func (a *application) dispatchFromBody(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if !decode(w, r, &req) {
		return
	}
	a.dispatchNotification(w, r, req)
}

func (a *application) dispatchNotification(w http.ResponseWriter, r *http.Request, req sendRequest) {
	p, personal := r.Context().Value(personalSenderContext{}).(personalSender)
	senderID, _ := r.Context().Value(webSenderContext{}).(string)
	if personal {
		senderID = p.UserID
	}
	if personal {
		if req.Topic != "" || req.Broadcast || (req.UserID != "" && req.UserID != p.UserID) {
			failure(w, 403, "API-Schlüssel erlaubt nur das eigene Konto")
			return
		}
		if req.UserID == "" && req.DeviceID == "" {
			req.UserID = p.UserID
		}
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		req.Title = "Push"
	}
	if strings.TrimSpace(req.Body) == "" || len(req.Body) > 2000 || len(req.Title) > 160 || len(req.Tag) > 100 || len(req.LongText) > 256*1024 || !utf8.ValidString(req.LongText) {
		failure(w, 400, "Nachricht ungültig oder zu lang")
		return
	}
	if req.ID != "" {
		failure(w, 400, "id darf nicht gesetzt werden")
		return
	}
	count := 0
	for _, v := range []string{req.Topic, req.UserID, req.DeviceID} {
		if v != "" {
			count++
		}
	}
	if req.Broadcast {
		count++
	}
	if req.DeviceIDs != nil && (req.UserID == "" || len(req.DeviceIDs) == 0) {
		failure(w, 400, "Geräteauswahl benötigt Benutzer und Geräte")
		return
	}
	if count != 1 {
		failure(w, 400, "Genau eines von topic, userId oder deviceId erforderlich")
		return
	}
	if req.Topic != "" && !topicRE.MatchString(req.Topic) {
		failure(w, 400, "Ungültiges Topic")
		return
	}
	id, err := secret()
	if err != nil {
		failure(w, 500, "Zufallsquelle fehlgeschlagen")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key != "" && !idempotencyKeyRE.MatchString(key) {
		failure(w, 400, "Idempotency-Key muss 12-128 Zeichen (A-Z, a-z, 0-9, ._:-) haben")
		return
	}
	inputJSON, _ := json.Marshal(req)
	sum := sha256.Sum256(inputJSON)
	payloadHash := hex.EncodeToString(sum[:])
	keyHash := invitationHash(key)
	if hash, ok := r.Context().Value(integrationSenderContext{}).(string); ok {
		keyHash = invitationHash("integration:" + hash + ":" + key)
	}
	if personal {
		keyHash = invitationHash("personal:" + p.UserID + ":" + key)
	}
	req.notification.ID = id
	pushContent := req.notification
	pushContent.LongText = "" // full text is stored in SQLite, not sent through APNs/FCM
	// Precheck payload size including confirmation token to avoid storing jobs
	// that can never be sent by common Web Push services.
	if sample, err := jsonNotification(pushContent, strings.Repeat("x", 43), strings.Repeat("x", 43), a.cfg.origin); err != nil || len(sample) > 3000 {
		failure(w, 413, "Push-Nutzdaten zu groß")
		return
	}
	now := time.Now().UTC()
	result := sendResult{MessageID: id, StatusURL: "/api/send/status/" + id}
	replayed := false
	err = a.db.update(func(db *database) error {
		if hash, ok := r.Context().Value(integrationSenderContext{}).(string); ok && !integrationKeyValid(db.IntegrationKeys, hash) {
			return errors.New("key revoked")
		}
		if personal {
			if !personalKeyValid(db.Users[p.UserID], p.KeyHash) {
				return errors.New("key revoked")
			}
			if req.DeviceID != "" {
				dev := db.Devices[req.DeviceID]
				if dev == nil || dev.UserID != p.UserID {
					return errors.New("target missing")
				}
			}
		}
		if key != "" {
			if prior, exists := db.Idempotency[keyHash]; exists && prior.CreatedAt.After(now.Add(-7*24*time.Hour)) {
				if prior.PayloadHash != payloadHash {
					return errors.New("idempotency conflict")
				}
				result = prior.Result
				replayed = true
				return nil
			}
		}
		switch {
		case req.Topic != "" && db.Topics[req.Topic] == nil:
			return errors.New("target missing")
		case req.UserID != "" && db.Users[req.UserID] == nil:
			return errors.New("target missing")
		case req.DeviceID != "" && db.Devices[req.DeviceID] == nil:
			return errors.New("target missing")
		}
		selected := map[string]bool{}
		for _, id := range req.DeviceIDs {
			dev := db.Devices[id]
			if dev == nil || dev.UserID != req.UserID {
				return errors.New("target missing")
			}
			selected[id] = true
		}
		targets := map[string]bool{}
		if req.Broadcast {
			for id := range db.Users {
				targets[id] = true
			}
		} else if req.Topic != "" {
			for _, u := range db.Users {
				if hasString(u.Topics, req.Topic) {
					targets[u.ID] = true
				}
			}
		} else if req.UserID != "" {
			targets[req.UserID] = true
		} else if dev := db.Devices[req.DeviceID]; dev != nil && db.Users[dev.UserID] != nil {
			targets[dev.UserID] = true
		}
		pending, pendingByUser := 0, map[string]int{}
		for _, job := range db.Deliveries {
			if !terminalDelivery(job.Status) {
				pending++
				pendingByUser[job.UserID]++
			}
		}
		for _, dev := range db.Devices {
			if (req.DeviceIDs != nil && !selected[dev.ID]) || !targets[dev.UserID] || (req.DeviceID != "" && req.DeviceID != dev.ID) {
				continue
			}
			pending++
			pendingByUser[dev.UserID]++
			if pending > maxPendingDeliveries || pendingByUser[dev.UserID] > maxPendingPerUser {
				return errors.New("queue full")
			}
		}
		for userID := range targets {
			message := inboxMessage{ID: id, Title: req.Title, Body: req.Body, Topic: req.Topic, Tag: req.Tag, LongText: req.LongText, CreatedAt: now}
			message.SenderUserID = senderID
			list := append(db.Messages[userID], message)
			if len(list) > maxInboxMessages {
				list = list[len(list)-maxInboxMessages:]
			}
			db.Messages[userID] = list
			result.InboxUsers++
		}
		for _, dev := range db.Devices {
			if (req.DeviceIDs != nil && !selected[dev.ID]) || !targets[dev.UserID] || (req.DeviceID != "" && req.DeviceID != dev.ID) {
				continue
			}
			jobID, e1 := secret()
			receipt, e2 := secret()
			if e1 != nil || e2 != nil {
				return errors.New("random source")
			}
			db.Deliveries[jobID] = &delivery{
				ID: jobID, MessageID: id, UserID: dev.UserID, DeviceID: dev.ID, DeviceName: dev.Name,
				Content: pushContent, ReceiptSecret: receipt, Status: "pending", NextAttemptAt: now,
				CreatedAt: now, ExpiresAt: now.Add(deliveryLifetime),
			}
			db.Deliveries[jobID].SenderUserID = senderID
			result.Recipients++
			result.Queued++
		}
		if !db.MessageLogDisabled {
			record := &messageLogRecord{Notification: req.notification, CreatedAt: now, Sender: logParty{ID: senderID, Name: "Server API"}, Source: "server_token", Target: logTarget{Topic: req.Topic, UserID: req.UserID, DeviceID: req.DeviceID, Broadcast: req.Broadcast, DeviceIDs: req.DeviceIDs}}
			if u := db.Users[senderID]; u != nil {
				record.Sender.Name = u.Name
				record.Source = "web"
			}
			if personal {
				record.Source = "personal_key"
			}
			if hash, ok := r.Context().Value(integrationSenderContext{}).(string); ok {
				record.Source = "integration_key"
				record.Sender.Name = "Integration"
				for _, k := range db.IntegrationKeys {
					if k.Hash == hash {
						record.Sender.Name = k.Name
					}
				}
			}
			for uid := range targets {
				record.Recipients = append(record.Recipients, logParty{ID: uid, Name: db.Users[uid].Name})
			}
			sort.Slice(record.Recipients, func(i, j int) bool { return record.Recipients[i].ID < record.Recipients[j].ID })
			record.Deliveries = deliveryViews(*db, id, "")
			db.MessageLog[id] = record
		}
		// Bounded cleanup of COMPLETED jobs only; keep pending jobs across restarts.
		for jobID, job := range db.Deliveries {
			if (job.Status == "accepted" || job.Status == "confirmed" || job.Status == "failed" || job.Status == "unconfirmed") && job.CreatedAt.Before(now.Add(-30*24*time.Hour)) {
				delete(db.Deliveries, jobID)
			}
		}
		for oldKey, record := range db.Idempotency {
			if record.CreatedAt.Before(now.Add(-7 * 24 * time.Hour)) {
				delete(db.Idempotency, oldKey)
			}
		}
		if key != "" {
			db.Idempotency[keyHash] = idempotencyRecord{Result: result, PayloadHash: payloadHash, CreatedAt: now}
		}
		return nil
	})
	if err != nil {
		if err.Error() == "key revoked" {
			failure(w, 401, "API-Schlüssel ungültig")
		} else if err.Error() == "queue full" {
			w.Header().Set("Retry-After", "60")
			failure(w, 429, "Zustellwarteschlange voll")
		} else if err.Error() == "idempotency conflict" {
			failure(w, 409, "Idempotency-Key bereits für andere Nachricht verwendet")
		} else if err.Error() == "target missing" {
			failure(w, 404, "Ziel nicht gefunden")
		} else {
			log.Printf("persist push queue: %v", err)
			failure(w, 500, "Nachricht konnte nicht gespeichert werden")
		}
		return
	}
	// No device network IO in the request. Returned 200 means durable queueing,
	// not accepted by a provider, confirmed by a browser, or read by a human.
	if replayed {
		w.Header().Set("Idempotent-Replay", "true")
	}
	reply(w, 200, result)
}
