package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"
)

// The challenge is specific to the authenticated session. Diagnostic device IDs
// and a shared/synced passkey alone cannot establish device possession.
func validDeviceActivityProof(d *device, userID, challenge, proof string) bool {
	if d == nil || d.UserID != userID || challenge == "" {
		return false
	}
	key, err := base64.RawURLEncoding.DecodeString(d.Subscription.Keys.Auth)
	if err != nil || len(key) != 16 {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(proof)
	if err != nil || len(signature) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("pushie-device-activity-v1:" + challenge + ":" + d.ID))
	return hmac.Equal(signature, mac.Sum(nil))
}

func (a *application) deviceActivityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/me/device-activity", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !a.rateLimit(w, r) {
			return
		}
		var in struct {
			DeviceID string `json:"deviceId"`
			Proof    string `json:"proof"`
		}
		if !decode(w, r, &in) {
			return
		}
		challenge := sessionHash(a.sessionToken(r))
		now := time.Now().UTC()
		a.db.mu.RLock()
		d := a.db.db.Devices[in.DeviceID]
		valid := validDeviceActivityProof(d, u.ID, challenge, in.Proof)
		recent := valid && d.LastActivityAt != nil && now.Sub(*d.LastActivityAt) < time.Minute
		a.db.mu.RUnlock()
		if !valid {
			failure(w, 403, "Gerätezuordnung nicht verifiziert")
			return
		}
		if !recent {
			invalid := errors.New("device changed")
			err := a.db.update(func(db *database) error {
				d := db.Devices[in.DeviceID]
				if !validDeviceActivityProof(d, u.ID, challenge, in.Proof) {
					return invalid
				}
				if d.LastActivityAt == nil || now.Sub(*d.LastActivityAt) >= time.Minute {
					d.LastActivityAt = &now
				}
				return nil
			})
			if errors.Is(err, invalid) {
				failure(w, 403, "Gerätezuordnung nicht verifiziert")
				return
			}
			if err != nil {
				failure(w, 500, "Speichern fehlgeschlagen")
				return
			}
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
}
