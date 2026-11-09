package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type logParty struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type logTarget struct {
	Topic     string   `json:"topic,omitempty"`
	UserID    string   `json:"userId,omitempty"`
	DeviceID  string   `json:"deviceId,omitempty"`
	DeviceIDs []string `json:"deviceIds,omitempty"`
	Broadcast bool     `json:"broadcast,omitempty"`
}
type messageLogRecord struct {
	Notification notification   `json:"notification"`
	CreatedAt    time.Time      `json:"createdAt"`
	Sender       logParty       `json:"sender"`
	Source       string         `json:"source"`
	Target       logTarget      `json:"target"`
	Recipients   []logParty     `json:"recipients"`
	Deliveries   []deliveryView `json:"deliveries"`
	Recovered    bool           `json:"recovered,omitempty"`
}

// Historical records contain only information that is still available locally.
func backfillMessageLog(db *database) {
	for uid, list := range db.Messages {
		for _, m := range list {
			rec := db.MessageLog[m.ID]
			if rec == nil {
				sender := logParty{ID: m.SenderUserID, Name: "Unknown (historical)"}
				if u := db.Users[m.SenderUserID]; u != nil {
					sender.Name = u.Name
				}
				rec = &messageLogRecord{Notification: notification{ID: m.ID, Title: m.Title, Body: m.Body, LongText: m.LongText, Topic: m.Topic, Tag: m.Tag}, CreatedAt: m.CreatedAt, Sender: sender, Source: "historical", Target: logTarget{Topic: m.Topic}, Recovered: true}
				db.MessageLog[m.ID] = rec
			}
			if u := db.Users[uid]; u != nil {
				rec.Recipients = append(rec.Recipients, logParty{ID: uid, Name: u.Name})
			}
		}
	}
	for _, d := range db.Deliveries {
		rec := db.MessageLog[d.MessageID]
		if rec == nil {
			rec = &messageLogRecord{Notification: d.Content, CreatedAt: d.CreatedAt, Sender: logParty{ID: d.SenderUserID, Name: "Unknown (historical)"}, Source: "historical", Recovered: true}
			rec.Notification.ID = d.MessageID
			if u := db.Users[d.SenderUserID]; u != nil {
				rec.Sender.Name = u.Name
			}
			db.MessageLog[d.MessageID] = rec
		}
		found := false
		for _, p := range rec.Recipients {
			if p.ID == d.UserID {
				found = true
			}
		}
		if !found {
			p := logParty{ID: d.UserID, Name: d.UserID}
			if u := db.Users[d.UserID]; u != nil {
				p.Name = u.Name
			}
			rec.Recipients = append(rec.Recipients, p)
		}
	}
	refreshMessageLog(db)
}

func refreshMessageLog(db *database) {
	if db.MessageLogDisabled {
		return
	}
	for _, d := range db.Deliveries {
		if rec := db.MessageLog[d.MessageID]; rec != nil {
			v := viewDelivery(d)
			found := false
			for i, old := range rec.Deliveries {
				if old.DeviceID == d.DeviceID {
					rec.Deliveries[i] = v
					found = true
					break
				}
			}
			if !found {
				rec.Deliveries = append(rec.Deliveries, v)
			}
		}
	}
}

func (a *application) messageLogRoutes(mux *http.ServeMux) {
	guard := func(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
			if !u.isAdmin() {
				failure(w, 403, "Nur für Administratoren")
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("GET /api/admin/message-log/settings", guard(func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]bool{"enabled": !a.db.snapshot().MessageLogDisabled})
	}))
	mux.HandleFunc("PUT /api/admin/message-log/settings", guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.Enabled == nil {
			failure(w, 400, "enabled erforderlich")
			return
		}
		if err := a.db.update(func(db *database) error { db.MessageLogDisabled = !*req.Enabled; return nil }); err != nil {
			failure(w, 500, "Einstellung konnte nicht gespeichert werden")
			return
		}
		reply(w, 200, map[string]bool{"enabled": *req.Enabled})
	}))
	cleanup := guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Days    int    `json:"days"`
			Confirm string `json:"confirm"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.Days < 0 || req.Days > 3650 {
			failure(w, 400, "Tage müssen zwischen 0 und 3650 liegen")
			return
		}
		cutoff := time.Now().UTC().Add(-time.Duration(req.Days) * 24 * time.Hour)
		count := 0
		clean := func(db *database, remove bool) {
			for id, rec := range db.MessageLog {
				if req.Days == 0 || rec.CreatedAt.Before(cutoff) {
					count++
					if remove {
						delete(db.MessageLog, id)
					}
				}
			}
		}
		if r.URL.Path == "/api/admin/message-log/cleanup" {
			if req.Confirm != "LÖSCHEN" {
				failure(w, 400, "Löschbestätigung erforderlich")
				return
			}
			if err := a.db.update(func(db *database) error { clean(db, true); return nil }); err != nil {
				failure(w, 500, "Log konnte nicht bereinigt werden")
				return
			}
		} else {
			snap := a.db.snapshot()
			clean(&snap, false)
		}
		reply(w, 200, map[string]int{"entries": count})
	})
	mux.HandleFunc("POST /api/admin/message-log/cleanup-preview", cleanup)
	mux.HandleFunc("POST /api/admin/message-log/cleanup", cleanup)
	mux.HandleFunc("GET /api/admin/message-log/{id}", guard(func(w http.ResponseWriter, r *http.Request) {
		snap := a.db.snapshot()
		record := snap.MessageLog[r.PathValue("id")]
		if record == nil {
			failure(w, 404, "Nachricht nicht gefunden")
			return
		}
		reply(w, 200, record)
	}))
	mux.HandleFunc("GET /api/admin/message-log", guard(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		from, to := q.Get("from"), q.Get("to")
		for _, date := range []string{from, to} {
			if date != "" {
				if _, err := time.Parse("2006-01-02", date); err != nil {
					failure(w, 400, "Invalid date")
					return
				}
			}
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		if offset < 0 {
			offset = 0
		}
		snap := a.db.snapshot()
		out := make([]messageLogRecord, 0)
		matches := func(value, filter string) bool {
			return strings.Contains(strings.ToLower(value), strings.ToLower(filter))
		}
		for _, rec := range snap.MessageLog {
			date := rec.CreatedAt.UTC().Format("2006-01-02")
			if from != "" && date < from || to != "" && date > to {
				continue
			}
			if !matches(rec.Sender.Name+" "+rec.Sender.ID+" "+rec.Source, q.Get("sender")) {
				continue
			}
			recipients := rec.Target.Topic + " " + rec.Target.UserID + " " + rec.Target.DeviceID
			if rec.Target.Broadcast {
				recipients += " broadcast"
			}
			for _, p := range rec.Recipients {
				recipients += " " + p.Name + " " + p.ID
			}
			if !matches(recipients, q.Get("recipient")) {
				continue
			}
			n := rec.Notification
			if !matches(n.Title+" "+n.Body+" "+n.LongText+" "+n.ID+" "+n.Tag, q.Get("q")) {
				continue
			}
			status := q.Get("status")
			found := status == "" || status == "no_devices" && len(rec.Deliveries) == 0
			for _, d := range rec.Deliveries {
				if d.Status == status {
					found = true
				}
			}
			if !found {
				continue
			}
			summary := *rec
			summary.Notification.LongText = ""
			out = append(out, summary)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].CreatedAt.Equal(out[j].CreatedAt) {
				return out[i].Notification.ID > out[j].Notification.ID
			}
			return out[i].CreatedAt.After(out[j].CreatedAt)
		})
		total := len(out)
		if offset > total {
			offset = total
		}
		end := offset + 50
		if end > total {
			end = total
		}
		reply(w, 200, map[string]any{"messages": out[offset:end], "total": total, "offset": offset})
	}))
}
