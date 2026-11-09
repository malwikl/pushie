package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	webpush "github.com/SherClockHolmes/webpush-go"
)

var topicRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

func (a *application) userRoutes(mux *http.ServeMux) {
	a.personalKeyRoutes(mux)
	mux.HandleFunc("PUT /api/me/settings", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !a.rateLimit(w, r) {
			return
		}
		var req struct {
			Language *string `json:"language"`
			Theme    *string `json:"theme"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.Language != nil && *req.Language != "de" && *req.Language != "en" {
			failure(w, 400, "Ungültige Sprache")
			return
		}
		if req.Theme != nil && *req.Theme != "auto" && *req.Theme != "light" && *req.Theme != "dark" {
			failure(w, 400, "Ungültiges Design")
			return
		}
		if req.Language == nil && req.Theme == nil {
			failure(w, 400, "Keine Einstellungen angegeben")
			return
		}
		if err := a.db.update(func(db *database) error {
			account := db.Users[u.ID]
			if account == nil {
				return errors.New("user missing")
			}
			if req.Language != nil {
				account.Language = *req.Language
			}
			if req.Theme != nil {
				account.Theme = *req.Theme
				if account.Theme == "auto" {
					account.Theme = ""
				}
			}
			return nil
		}); err != nil {
			failure(w, 500, "Speichern fehlgeschlagen")
			return
		}
		saved := a.db.userByID(u.ID)
		reply(w, 200, map[string]string{"language": saved.Language, "theme": saved.Theme})
	}))
	mux.HandleFunc("GET /api/me", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		snap := a.db.userSnapshot(u.ID, false)
		topics := make([]topic, 0, len(snap.Topics))
		for _, t := range snap.Topics {
			topics = append(topics, *t)
		}
		sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
		devices := make([]map[string]any, 0)
		for _, d := range snap.Devices {
			if d.UserID == u.ID {
				devices = append(devices, map[string]any{"id": d.ID, "name": d.Name, "createdAt": d.CreatedAt})
			}
		}
		sort.Slice(devices, func(i, j int) bool { return devices[i]["name"].(string) < devices[j]["name"].(string) })
		if u.Topics == nil {
			u.Topics = []string{}
		}
		reply(w, 200, map[string]any{"id": u.ID, "name": u.Name, "topics": topics, "subscriptions": u.Topics, "devices": devices, "passkeyCount": len(u.Credentials), "role": u.Role, "language": u.Language, "theme": u.Theme})
	}))
	mux.HandleFunc("DELETE /api/me/sent-messages/{id}", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		count := 0
		err := a.db.update(func(db *database) error {
			count = hideSentMessages(db, u.ID, r.PathValue("id"))
			return nil
		})
		if err != nil {
			failure(w, 500, "Speichern fehlgeschlagen")
			return
		}
		if count == 0 {
			failure(w, 404, "Nachricht nicht gefunden")
			return
		}
		reply(w, 200, map[string]int{"deleted": count})
	}))
	mux.HandleFunc("POST /api/me/sent-messages/cleanup", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		var req struct {
			Confirm string `json:"confirm"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.Confirm != "LÖSCHEN" {
			failure(w, 400, "Bestätigung erforderlich")
			return
		}
		count := 0
		if err := a.db.update(func(db *database) error { count = hideSentMessages(db, u.ID, ""); return nil }); err != nil {
			failure(w, 500, "Speichern fehlgeschlagen")
			return
		}
		reply(w, 200, map[string]int{"deleted": count})
	}))
	mux.HandleFunc("GET /api/me/sent-messages", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		snap := a.db.snapshot()
		type sentView struct {
			inboxMessage
			Deliveries []deliveryView `json:"deliveries"`
		}
		messages := map[string]inboxMessage{}
		for _, list := range snap.Messages {
			for _, m := range list {
				if m.SenderUserID == u.ID && !m.SentDeleted {
					m.ReadAt = nil
					messages[m.ID] = m
				}
			}
		}
		for _, d := range snap.Deliveries {
			if d.SenderUserID == u.ID && !d.SentDeleted {
				if _, ok := messages[d.MessageID]; !ok {
					messages[d.MessageID] = inboxMessage{ID: d.MessageID, Title: d.Content.Title, Body: d.Content.Body, LongText: d.Content.LongText, Topic: d.Content.Topic, CreatedAt: d.CreatedAt}
				}
			}
		}
		out := make([]sentView, 0, len(messages))
		for _, m := range messages {
			out = append(out, sentView{m, deliveryViews(snap, m.ID, "")})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
		if len(out) > maxInboxMessages {
			out = out[:maxInboxMessages]
		}
		reply(w, 200, map[string]any{"messages": out})
	}))
	mux.HandleFunc("GET /api/me/messages", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		snap := a.db.userSnapshot(u.ID, true)
		msgs := snap.Messages[u.ID]
		unread := 0
		for _, m := range msgs {
			if m.ReadAt == nil {
				unread++
			}
		}
		// Stored oldest-first, display newest-first. Data is limited to 200 per account.
		type inboxView struct {
			inboxMessage
			Deliveries []deliveryView `json:"deliveries"`
		}
		out := make([]inboxView, 0, len(msgs))
		for i := len(msgs) - 1; i >= 0; i-- {
			out = append(out, inboxView{inboxMessage: msgs[i], Deliveries: deliveryViews(snap, msgs[i].ID, u.ID)})
		}
		reply(w, 200, map[string]any{"messages": out, "unread": unread})
	}))
	mux.HandleFunc("PUT /api/me/messages/{id}/read", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		id := r.PathValue("id")
		err := a.db.update(func(d *database) error {
			for i := range d.Messages[u.ID] {
				if d.Messages[u.ID][i].ID == id {
					if d.Messages[u.ID][i].ReadAt == nil {
						now := time.Now().UTC()
						d.Messages[u.ID][i].ReadAt = &now
					}
					return nil
				}
			}
			return errors.New("not found")
		})
		if err != nil {
			failure(w, 404, "Nachricht nicht gefunden")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("POST /api/me/messages/read-all", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		err := a.db.update(func(d *database) error {
			now := time.Now().UTC()
			for i := range d.Messages[u.ID] {
				if d.Messages[u.ID][i].ReadAt == nil {
					d.Messages[u.ID][i].ReadAt = &now
				}
			}
			return nil
		})
		if err != nil {
			failure(w, 500, "Speichern fehlgeschlagen")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("DELETE /api/me/messages/{id}", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		id := r.PathValue("id")
		err := a.db.update(func(d *database) error {
			for i, m := range d.Messages[u.ID] {
				if m.ID == id {
					if m.SenderUserID == u.ID {
						// Every source used to reconstruct sent history must remember
						// the deletion, including jobs that are still being delivered.
						for uid, list := range d.Messages {
							for j := range list {
								if list[j].ID == id && list[j].SenderUserID == u.ID {
									d.Messages[uid][j].SentDeleted = true
								}
							}
						}
						for _, job := range d.Deliveries {
							if job.MessageID == id && job.SenderUserID == u.ID {
								job.SentDeleted = true
							}
						}
					}
					d.Messages[u.ID] = append(d.Messages[u.ID][:i], d.Messages[u.ID][i+1:]...)
					return nil
				}
			}
			return errors.New("not found")
		})
		if err != nil {
			failure(w, 404, "Nachricht nicht gefunden")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("PUT /api/me/topics/{topic}", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) { a.updateTopic(w, r, u, true) }))
	mux.HandleFunc("DELETE /api/me/topics/{topic}", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) { a.updateTopic(w, r, u, false) }))
	mux.HandleFunc("POST /api/me/devices", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !a.rateLimit(w, r) {
			return
		}
		var in struct {
			Name         string          `json:"name"`
			Subscription json.RawMessage `json:"subscription"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		var subscription webpush.Subscription
		if json.Unmarshal(in.Subscription, &subscription) != nil || !validName(in.Name) || !validSubscription(subscription) {
			failure(w, 400, "Gerätename oder PushSubscription ungültig")
			return
		}
		id, err := secret()
		if err != nil {
			failure(w, 500, "Zufallsquelle fehlgeschlagen")
			return
		}
		err = a.db.update(func(d *database) error {
			if d.Users[u.ID] == nil {
				return errors.New("unknown user")
			}
			for _, device := range d.Devices {
				if device.Subscription.Endpoint == subscription.Endpoint {
					if device.UserID != u.ID {
						return errors.New("device belongs to another user")
					}
					device.Subscription = subscription
					device.Name = in.Name
					id = device.ID
					return nil
				}
			}
			count := 0
			for _, dev := range d.Devices {
				if dev.UserID == u.ID {
					count++
				}
			}
			if count >= maxDevicesPerUser {
				return errors.New("device quota")
			}
			d.Devices[id] = &device{ID: id, UserID: u.ID, Name: in.Name, Subscription: subscription, CreatedAt: time.Now().UTC()}
			return nil
		})
		if err != nil {
			if err.Error() == "device quota" {
				failure(w, 409, "Gerätelimit erreicht")
				return
			}
			failure(w, 409, "Gerät konnte nicht registriert werden: Endpoint ist belegt")
			return
		}
		reply(w, 201, map[string]string{"id": id})
	}))
	mux.HandleFunc("DELETE /api/me/devices/{id}", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !a.rateLimit(w, r) {
			return
		}
		id := r.PathValue("id")
		err := a.db.update(func(d *database) error {
			dev := d.Devices[id]
			if dev == nil || dev.UserID != u.ID {
				return errors.New("not found")
			}
			delete(d.Devices, id)
			return nil
		})
		if err != nil {
			failure(w, 404, "Gerät nicht gefunden")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
}
func (a *application) updateTopic(w http.ResponseWriter, r *http.Request, u *user, sub bool) {
	if !a.rateLimit(w, r) {
		return
	}
	name := r.PathValue("topic")
	if !topicRE.MatchString(name) {
		failure(w, 400, "Ungültiges Topic")
		return
	}
	err := a.db.update(func(d *database) error {
		if d.Topics[name] == nil {
			return errors.New("unknown topic")
		}
		account := d.Users[u.ID]
		if account == nil {
			return errors.New("unknown user")
		}
		if sub {
			if !hasString(account.Topics, name) {
				account.Topics = append(account.Topics, name)
				sort.Strings(account.Topics)
			}
		} else {
			var next []string
			for _, x := range account.Topics {
				if x != name {
					next = append(next, x)
				}
			}
			account.Topics = next
		}
		return nil
	})
	if err != nil {
		failure(w, 404, "Topic nicht gefunden")
		return
	}
	reply(w, 200, map[string]bool{"subscribed": sub})
}
func validName(name string) bool {
	return utf8.ValidString(name) && len([]rune(name)) >= 1 && len([]rune(name)) <= 80 && len(name) <= 240
}
func validSubscription(s webpush.Subscription) bool {
	if !validPushEndpoint(s.Endpoint) || len(s.Endpoint) > 2048 {
		return false
	}
	auth, err1 := base64.RawURLEncoding.DecodeString(s.Keys.Auth)
	dh, err2 := base64.RawURLEncoding.DecodeString(s.Keys.P256dh)
	return err1 == nil && err2 == nil && len(auth) == 16 && len(dh) == 65 && dh[0] == 4
}
func (a *application) adminRoutes(mux *http.ServeMux) {
	a.integrationKeyRoutes(mux)
	a.messageLogRoutes(mux)
	mux.HandleFunc("GET /api/admin/deliveries/{id}", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		a.deliveryStatus(w, r, r.PathValue("id"))
	}))
	mux.HandleFunc("GET /api/admin/targets", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		snap := a.db.snapshot()
		users := make([]map[string]string, 0, len(snap.Users))
		topics := make([]topic, 0, len(snap.Topics))
		for _, u := range snap.Users {
			users = append(users, map[string]string{"id": u.ID, "name": u.Name})
		}
		sort.Slice(users, func(i, j int) bool { return users[i]["name"] < users[j]["name"] })
		for _, t := range snap.Topics {
			topics = append(topics, *t)
		}
		sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
		devices := make([]map[string]string, 0)
		for _, d := range snap.Devices {
			devices = append(devices, map[string]string{"id": d.ID, "userId": d.UserID, "name": d.Name})
		}
		reply(w, 200, map[string]any{"users": users, "topics": topics, "devices": devices})
	}))
	mux.HandleFunc("POST /api/admin/send", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		if !a.rateLimit(w, r) {
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), webSenderContext{}, u.ID))
		a.dispatchFromBody(w, r)
	}))
	mux.HandleFunc("POST /api/admin/invites/web", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		if !a.rateLimit(w, r) {
			return
		}
		a.createInvite(w, r)
	}))
	mux.HandleFunc("POST /api/admin/topics/web", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		a.createTopic(w, r)
	}))

	mux.HandleFunc("DELETE /api/admin/topics/{topic}/web", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		name := r.PathValue("topic")
		err := a.db.update(func(d *database) error {
			if d.Topics[name] == nil {
				return errors.New("unknown topic")
			}
			delete(d.Topics, name)
			for _, account := range d.Users {
				next := make([]string, 0, len(account.Topics))
				for _, subscribed := range account.Topics {
					if subscribed != name {
						next = append(next, subscribed)
					}
				}
				account.Topics = next
			}
			return nil
		})
		if err != nil {
			failure(w, 404, "Topic nicht gefunden")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("GET /api/admin/users/web", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		snap := a.db.snapshot()
		users := make([]map[string]any, 0, len(snap.Users))
		for _, account := range snap.Users {
			count := 0
			for _, dev := range snap.Devices {
				if dev.UserID == account.ID {
					count++
				}
			}
			users = append(users, map[string]any{"id": account.ID, "name": account.Name, "role": account.Role, "devices": count})
		}
		sort.Slice(users, func(i, j int) bool { return users[i]["name"].(string) < users[j]["name"].(string) })
		reply(w, 200, map[string]any{"users": users})
	}))
	mux.HandleFunc("POST /api/admin/users/{id}/promote/web", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		err := a.db.update(func(d *database) error {
			account := d.Users[r.PathValue("id")]
			if account == nil {
				return errors.New("unknown user")
			}
			account.Role = "admin"
			return nil
		})
		if err != nil {
			failure(w, 404, "Benutzer nicht gefunden")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("DELETE /api/admin/users/{id}/web", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		id := r.PathValue("id")
		if id == u.ID {
			failure(w, 409, "Das eigene Konto kann nicht gelöscht werden.")
			return
		}
		err := a.db.update(func(d *database) error {
			if d.Users[id] == nil {
				return errors.New("unknown user")
			}
			delete(d.Users, id)
			delete(d.Messages, id)
			for key, dev := range d.Devices {
				if dev.UserID == id {
					delete(d.Devices, key)
				}
			}
			for key, job := range d.Deliveries {
				if job.UserID == id {
					delete(d.Deliveries, key)
				}
			}
			return nil
		})
		if err != nil {
			failure(w, 404, "Benutzer nicht gefunden")
			return
		}
		a.auth.mu.Lock()
		for key, session := range a.auth.sessions {
			if session.UserID == id {
				delete(a.auth.sessions, key)
			}
		}
		for key, ceremony := range a.auth.ceremonies {
			if ceremony.User != nil && ceremony.User.ID == id {
				delete(a.auth.ceremonies, key)
			}
		}
		a.auth.mu.Unlock()
		reply(w, 200, map[string]bool{"ok": true})
	}))

	admin := func(w http.ResponseWriter, r *http.Request) bool {
		if !authorized(r, a.cfg.adminToken) {
			failure(w, 401, "Administrator-Token erforderlich")
			return false
		}
		if !a.rateLimit(w, r) {
			return false
		}
		markAuthorization(r, "", "admin_token")
		// No cookie authorization here. If a browser does supply Origin, require same origin.
		if o := r.Header.Get("Origin"); o != "" && o != a.cfg.origin {
			failure(w, 403, "Origin nicht erlaubt")
			return false
		}
		return true
	}
	mux.HandleFunc("POST /api/admin/invites", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		a.createInvite(w, r)
	})
	mux.HandleFunc("POST /api/admin/topics", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		a.createTopic(w, r)
	})
	mux.HandleFunc("POST /api/admin/users/{id}/promote", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		id := r.PathValue("id")
		err := a.db.update(func(d *database) error {
			u := d.Users[id]
			if u == nil {
				return errors.New("unknown user")
			}
			u.Role = "admin"
			return nil
		})
		if err != nil {
			failure(w, 404, "Benutzer nicht gefunden")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		snap := a.db.snapshot()
		users := make([]map[string]any, 0, len(snap.Users))
		for _, u := range snap.Users {
			count := 0
			for _, d := range snap.Devices {
				if d.UserID == u.ID {
					count++
				}
			}
			users = append(users, map[string]any{"id": u.ID, "name": u.Name, "devices": count})
		}
		sort.Slice(users, func(i, j int) bool { return users[i]["name"].(string) < users[j]["name"].(string) })
		reply(w, 200, map[string]any{"users": users})
	})
}

func (a *application) createInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !validName(in.Name) {
		failure(w, 400, "Name muss 1–80 Zeichen haben")
		return
	}
	token, err := secret()
	if err != nil {
		failure(w, 500, "Zufallsquelle fehlgeschlagen")
		return
	}
	expires := time.Now().UTC().Add(24 * time.Hour)
	err = a.db.update(func(d *database) error {
		for k, v := range d.Invites {
			if !v.Expires.After(time.Now()) {
				delete(d.Invites, k)
			}
		}
		d.Invites[invitationHash(token)] = &invitation{Name: in.Name, Expires: expires}
		return nil
	})
	if err != nil {
		failure(w, 500, "Einladung konnte nicht gespeichert werden")
		return
	}
	reply(w, 201, map[string]any{"invite": token, "expires": expires, "name": in.Name})
}
func (a *application) createTopic(w http.ResponseWriter, r *http.Request) {
	var in topic
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if !topicRE.MatchString(in.Name) || len(in.Description) > 200 {
		failure(w, 400, "Topic ungültig")
		return
	}
	err := a.db.update(func(d *database) error {
		if d.Topics[in.Name] != nil {
			return errors.New("topic exists")
		}
		d.Topics[in.Name] = &in
		return nil
	})
	if err != nil {
		failure(w, 409, "Topic existiert bereits")
		return
	}
	reply(w, 201, in)
}

// Mark every reconstruction source while preserving inboxes and delivery jobs.
func hideSentMessages(db *database, sender, id string) int {
	ids := map[string]bool{}
	for uid, list := range db.Messages {
		for i, m := range list {
			if m.SenderUserID == sender && (id == "" || m.ID == id) && !m.SentDeleted {
				ids[m.ID] = true
				db.Messages[uid][i].SentDeleted = true
			}
		}
	}
	for _, job := range db.Deliveries {
		if job.SenderUserID == sender && (id == "" || job.MessageID == id) && !job.SentDeleted {
			ids[job.MessageID] = true
			job.SentDeleted = true
		}
	}
	return len(ids)
}
