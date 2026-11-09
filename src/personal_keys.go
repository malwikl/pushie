package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Only the hash is persisted; the bearer secret is returned once at creation.
type personalAPIKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"createdAt"`
}
type personalKeyView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}
type personalSenderContext struct{}
type personalSender struct{ UserID, KeyHash string }

func personalKeyValid(u *user, hash string) bool {
	if u == nil {
		return false
	}
	for _, k := range u.APIKeys {
		if constantBytesEqual([]byte(k.Hash), []byte(hash)) {
			return true
		}
	}
	return false
}

// Shared server tokens retain their existing permissions. Personal tokens never
// create a browser session and apply even to administrators' own accounts only.
func (a *application) authorizeSend(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if !a.rateLimit(w, r) {
		return r, false
	}
	if authorized(r, a.cfg.sendToken) {
		markAuthorization(r, "", "server_token")
		return r, true
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		failure(w, 401, "API-Schlüssel ungültig")
		return r, false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "psk" || len(token) > 256 {
		failure(w, 401, "API-Schlüssel ungültig")
		return r, false
	}
	hash := invitationHash(token)
	a.db.mu.RLock()
	valid := personalKeyValid(a.db.db.Users[parts[1]], hash)
	a.db.mu.RUnlock()
	if !valid {
		failure(w, 401, "API-Schlüssel ungültig")
		return r, false
	}
	if !a.auth.allow("personal:"+parts[1], 90) {
		w.Header().Set("Retry-After", "60")
		failure(w, 429, "Zu viele Anfragen")
		return r, false
	}
	markAuthorization(r, parts[1], "personal_key")
	return r.WithContext(context.WithValue(r.Context(), personalSenderContext{}, personalSender{parts[1], hash})), true
}

func (a *application) personalKeyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me/keys", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		keys := make([]personalKeyView, 0, len(u.APIKeys))
		for _, k := range u.APIKeys {
			keys = append(keys, personalKeyView{k.ID, k.Name, k.CreatedAt})
		}
		reply(w, 200, map[string]any{"keys": keys})
	}))
	mux.HandleFunc("POST /api/me/keys", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !a.rateLimit(w, r) {
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || len(in.Name) > 80 || !utf8.ValidString(in.Name) {
			failure(w, 400, "Name erforderlich (max. 80 Bytes)")
			return
		}
		raw, err := secret()
		if err != nil {
			failure(w, 500, "Zufallsquelle fehlgeschlagen")
			return
		}
		id, err := secret()
		if err != nil {
			failure(w, 500, "Zufallsquelle fehlgeschlagen")
			return
		}
		token := "psk." + u.ID + "." + raw
		k := personalAPIKey{ID: id, Name: in.Name, Hash: invitationHash(token), CreatedAt: time.Now().UTC()}
		err = a.db.update(func(db *database) error {
			owner := db.Users[u.ID]
			if owner == nil {
				return errors.New("user missing")
			}
			if len(owner.APIKeys) >= 20 {
				return errors.New("key limit")
			}
			owner.APIKeys = append(owner.APIKeys, k)
			return nil
		})
		if err != nil {
			if err.Error() == "key limit" {
				failure(w, 409, "Maximal 20 API-Schlüssel pro Konto")
				return
			}
			failure(w, 500, "Speichern fehlgeschlagen")
			return
		}
		reply(w, 201, map[string]any{"key": personalKeyView{k.ID, k.Name, k.CreatedAt}, "token": token})
	}))
	mux.HandleFunc("DELETE /api/me/keys/{id}", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !a.rateLimit(w, r) {
			return
		}
		missing := errors.New("not found")
		err := a.db.update(func(db *database) error {
			owner := db.Users[u.ID]
			if owner == nil {
				return missing
			}
			for i, k := range owner.APIKeys {
				if k.ID == r.PathValue("id") {
					owner.APIKeys = append(owner.APIKeys[:i], owner.APIKeys[i+1:]...)
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
