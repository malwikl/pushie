package main

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

type integrationSenderContext struct{}

func integrationKeyValid(keys []personalAPIKey, hash string) bool {
	for _, key := range keys {
		if constantBytesEqual([]byte(key.Hash), []byte(hash)) {
			return true
		}
	}
	return false
}

func (a *application) integrationKeyRoutes(mux *http.ServeMux) {
	guard := func(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
			if !u.isAdmin() {
				failure(w, 403, "Nur für Administratoren")
				return
			}
			if r.Method != "GET" && !a.rateLimit(w, r) {
				return
			}
			fn(w, r)
		})
	}
	mux.HandleFunc("GET /api/admin/integration-keys", guard(func(w http.ResponseWriter, r *http.Request) {
		a.db.mu.RLock()
		keys := make([]map[string]any, 0, len(a.db.db.IntegrationKeys))
		for _, k := range a.db.db.IntegrationKeys {
			keys = append(keys, map[string]any{"id": k.ID, "name": k.Name, "createdAt": k.CreatedAt, "scope": "send"})
		}
		a.db.mu.RUnlock()
		reply(w, 200, map[string]any{"keys": keys})
	}))
	mux.HandleFunc("POST /api/admin/integration-keys", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name  string `json:"name"`
			Scope string `json:"scope"`
		}
		if !decode(w, r, &in) {
			return
		}
		// Accept old clients' scope values; every integration key has sending access.
		if in.Scope != "" && in.Scope != "watchtower" && in.Scope != "send" {
			failure(w, 400, "Ungültige Berechtigung")
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if !validName(in.Name) {
			failure(w, 400, "Name muss 1-80 Zeichen haben")
			return
		}
		id, err := secret()
		if err != nil {
			failure(w, 500, "Zufallsquelle fehlgeschlagen")
			return
		}
		raw, err := secret()
		if err != nil {
			failure(w, 500, "Zufallsquelle fehlgeschlagen")
			return
		}
		token := "isk." + raw
		key := personalAPIKey{Scope: "send", ID: id, Name: in.Name, Hash: invitationHash(token), CreatedAt: time.Now().UTC()}
		limit := errors.New("limit")
		err = a.db.update(func(db *database) error {
			if len(db.IntegrationKeys) >= 100 {
				return limit
			}
			db.IntegrationKeys = append(db.IntegrationKeys, key)
			return nil
		})
		if err != nil {
			if errors.Is(err, limit) {
				failure(w, 409, "Maximal 100 Integrationsschlüssel")
			} else {
				failure(w, 500, "Speichern fehlgeschlagen")
			}
			return
		}
		reply(w, 201, map[string]any{"key": personalKeyView{key.ID, key.Name, key.CreatedAt}, "token": token})
	}))
	mux.HandleFunc("DELETE /api/admin/integration-keys/{id}", guard(func(w http.ResponseWriter, r *http.Request) {
		missing := errors.New("missing")
		err := a.db.update(func(db *database) error {
			for i, key := range db.IntegrationKeys {
				if key.ID == r.PathValue("id") {
					db.IntegrationKeys = append(db.IntegrationKeys[:i], db.IntegrationKeys[i+1:]...)
					return nil
				}
			}
			return missing
		})
		if err != nil {
			if errors.Is(err, missing) {
				failure(w, 404, "API-Schlüssel nicht gefunden")
			} else {
				failure(w, 500, "Speichern fehlgeschlagen")
			}
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
}
