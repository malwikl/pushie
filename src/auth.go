package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

const ceremonyTTL = 5 * time.Minute
const loginTTL = 7 * 24 * time.Hour

type ceremony struct {
	Kind       string
	User       *user
	InviteHash string
	Session    webauthn.SessionData
	Expires    time.Time
}
type sessionInfo struct {
	UserID  string
	Expires time.Time
}
type limitInfo struct {
	Count   int
	Expires time.Time
}
type authState struct {
	mu         sync.Mutex
	ceremonies map[string]ceremony
	sessions   map[string]sessionInfo
	limits     map[string]limitInfo
}

func newAuthState() *authState {
	return &authState{ceremonies: map[string]ceremony{}, sessions: map[string]sessionInfo{}, limits: map[string]limitInfo{}}
}
func sessionHash(raw string) string { b := sha256.Sum256([]byte(raw)); return hex.EncodeToString(b[:]) }
func (a *authState) putCeremony(c ceremony) (string, error) {
	key, err := secret()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, v := range a.ceremonies {
		if !v.Expires.After(now) {
			delete(a.ceremonies, k)
		}
	}
	if len(a.ceremonies) >= 500 {
		return "", errors.New("too many pending challenges")
	}
	a.ceremonies[key] = c
	return key, nil
}
func (a *authState) takeCeremony(id, kind string) (ceremony, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.ceremonies[id]
	if ok {
		delete(a.ceremonies, id)
	}
	return c, ok && c.Kind == kind && c.Expires.After(time.Now())
}
func (a *authState) newSession(userID string) (string, error) {
	token, err := secret()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) > 1000 {
		for k, v := range a.sessions {
			if v.Expires.Before(time.Now()) {
				delete(a.sessions, k)
			}
		}
	}
	a.sessions[sessionHash(token)] = sessionInfo{userID, time.Now().Add(loginTTL)}
	return token, nil
}
func (a *authState) lookupSession(token string) string {
	if token == "" {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[sessionHash(token)]
	if !ok {
		return ""
	}
	if !s.Expires.After(time.Now()) {
		delete(a.sessions, sessionHash(token))
		return ""
	}
	return s.UserID
}
func (a *authState) revoke(token string) {
	a.mu.Lock()
	delete(a.sessions, sessionHash(token))
	a.mu.Unlock()
}
func (a *authState) allow(ip string, max int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.limits[ip]
	if time.Now().After(l.Expires) {
		l = limitInfo{Expires: time.Now().Add(time.Minute)}
	}
	l.Count++
	if len(a.limits) > 2000 {
		for k, v := range a.limits {
			if time.Now().After(v.Expires) {
				delete(a.limits, k)
			}
		}
	}
	if _, exists := a.limits[ip]; !exists && len(a.limits) >= 10000 {
		return false
	}
	a.limits[ip] = l
	return l.Count <= max
}
func (a *application) trustedProxy(ip netip.Addr) bool {
	for _, prefix := range a.cfg.trustedProxies {
		if prefix.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}
func (a *application) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	if !a.trustedProxy(peer) {
		return peer.Unmap().String()
	}
	// Walk right to left, stopping at the first untrusted hop. A trusted
	// proxy must append or overwrite X-Forwarded-For, never pass it unchanged.
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(parts) > 16 {
		return peer.Unmap().String()
	}
	current := peer
	for i := len(parts) - 1; i >= 0 && a.trustedProxy(current); i-- {
		next, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return peer.Unmap().String()
		}
		current = next.Unmap()
	}
	return current.Unmap().String()
}
func (a *application) rateLimit(w http.ResponseWriter, r *http.Request) bool {
	bucket, max := "write", 90
	if r.Method == http.MethodGet {
		bucket, max = "read", 120
	}
	if strings.HasPrefix(r.URL.Path, "/api/auth/") || strings.HasPrefix(r.URL.Path, "/api/me/passkeys") {
		bucket, max = "auth", 30
	}
	if r.URL.Path == "/api/push/ack" {
		bucket, max = "ack", 600
	}
	ip := a.clientIP(r)
	ipBudget := max
	id := a.auth.lookupSession(a.sessionToken(r))
	if bucket == "read" && id != "" {
		ipBudget = 1200
	}
	if !a.auth.allow("global:"+bucket, max*100) || !a.auth.allow(bucket+":"+ip, ipBudget) {
		w.Header().Set("Retry-After", "60")
		failure(w, 429, "Zu viele Anfragen")
		return false
	}
	if bucket == "write" || bucket == "read" {
		userBudget := max
		if bucket == "read" {
			userBudget = 600
		}
		if id != "" && !a.auth.allow("user:"+bucket+":"+id, userBudget) {
			w.Header().Set("Retry-After", "60")
			failure(w, 429, "Zu viele Anfragen")
			return false
		}
	}
	return true
}
func (a *application) cookieName() string {
	if a.cfg.secureCookies {
		return "__Host-webpush_session"
	}
	return "webpush_session_dev"
}
func (a *application) sessionToken(r *http.Request) string {
	c, err := r.Cookie(a.cookieName())
	if err != nil {
		return ""
	}
	return c.Value
}
func (a *application) currentUser(r *http.Request) *user {
	return a.db.userByID(a.auth.lookupSession(a.sessionToken(r)))
}
func (a *application) setSession(w http.ResponseWriter, userID string) error {
	token, err := a.auth.newSession(userID)
	if err != nil {
		return err
	}
	slog.Info("auth.session.created", "user_id", userID)
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: token, Path: "/", HttpOnly: true, Secure: a.cfg.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: int(loginTTL.Seconds())})
	return nil
}
func (a *application) clearSession(w http.ResponseWriter, r *http.Request) {
	slog.Info("auth.session.revoked", append(requestFields(r), "user_id", a.auth.lookupSession(a.sessionToken(r)))...)
	a.auth.revoke(a.sessionToken(r))
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: "", Path: "/", HttpOnly: true, Secure: a.cfg.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
func (a *application) requireUser(fn func(http.ResponseWriter, *http.Request, *user)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.currentUser(r)
		if u == nil {
			// Expired cookies must not leave a client appearing signed in after a restart.
			a.clearSession(w, r)
			w.Header().Set("Clear-Site-Data", `"cache"`)
			failure(w, 401, "Bitte anmelden")
			return
		}
		markAuthorization(r, u.ID, "session")
		if r.Method == "GET" && !a.rateLimit(w, r) {
			return
		}
		if r.Method != "GET" && !a.browserOrigin(w, r) {
			return
		}
		fn(w, r, u)
	}
}
func authRequest(r *http.Request, body json.RawMessage) *http.Request {
	out := r.Clone(r.Context())
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	return out
}

type ceremonyFinish struct {
	CeremonyID string          `json:"ceremonyId"`
	Response   json.RawMessage `json:"response"`
}

func (a *application) finishInput(w http.ResponseWriter, r *http.Request, kind string) (ceremony, ceremonyFinish, bool) {
	var in ceremonyFinish
	if !decode(w, r, &in) {
		return ceremony{}, in, false
	}
	if len(in.CeremonyID) < 20 || len(in.Response) == 0 {
		failure(w, 400, "Ungültige Anmeldung")
		return ceremony{}, in, false
	}
	c, ok := a.auth.takeCeremony(in.CeremonyID, kind)
	if !ok {
		failure(w, 401, "Anfrage abgelaufen oder bereits verwendet")
		return ceremony{}, in, false
	}
	return c, in, true
}
func (a *application) authRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/bootstrap/begin", func(w http.ResponseWriter, r *http.Request) {
		if !a.browserOrigin(w, r) || !a.rateLimit(w, r) {
			return
		}
		var in struct {
			Name string `json:"name"`
			Code string `json:"code"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if !validName(in.Name) || len(in.Code) > 256 || len(in.Code) < 20 || a.cfg.bootstrapOTP == "" || !constantBytesEqual([]byte(in.Code), []byte(a.cfg.bootstrapOTP)) || !a.db.bootstrapAvailable(a.cfg.bootstrapOTP) {
			failure(w, 403, "Ersteinrichtung nicht verfügbar oder Code ungültig")
			return
		}
		id, err := secret()
		if err != nil {
			failure(w, 500, "Zufallsquelle fehlgeschlagen")
			return
		}
		u := &user{ID: id, Name: in.Name, Role: "admin", CreatedAt: time.Now().UTC(), Credentials: []webauthn.Credential{}, Topics: []string{}}
		creation, sess, err := a.wa.BeginRegistration(u)
		if err != nil {
			failure(w, 500, "WebAuthn Initialisierung fehlgeschlagen")
			return
		}
		ceremonyID, err := a.auth.putCeremony(ceremony{Kind: "bootstrap", User: u, Session: *sess, Expires: time.Now().Add(ceremonyTTL)})
		if err != nil {
			failure(w, 503, "Zu viele Anfragen")
			return
		}
		reply(w, 200, map[string]any{"ceremonyId": ceremonyID, "options": creation})
	})
	mux.HandleFunc("POST /api/auth/bootstrap/finish", func(w http.ResponseWriter, r *http.Request) {
		if !a.browserOrigin(w, r) || !a.rateLimit(w, r) {
			return
		}
		c, in, ok := a.finishInput(w, r, "bootstrap")
		if !ok {
			return
		}
		cred, err := a.wa.FinishRegistration(c.User, c.Session, authRequest(r, in.Response))
		if err != nil {
			failure(w, 401, "Passkey-Verifikation fehlgeschlagen")
			return
		}
		c.User.Credentials = append(c.User.Credentials, *cred)
		if a.cfg.bootstrapOTP == "" {
			failure(w, 409, "Einmalige Ersteinrichtung deaktiviert")
			return
		}
		err = a.db.registerBootstrapAdmin(c.User)
		if err != nil {
			failure(w, 409, "Ersteinrichtung bereits abgeschlossen oder Speicherung fehlgeschlagen")
			return
		}
		if err = a.setSession(w, c.User.ID); err != nil {
			failure(w, 500, "Sitzung fehlgeschlagen")
			return
		}
		reply(w, 201, map[string]any{"name": c.User.Name, "role": "admin"})
	})
	mux.HandleFunc("POST /api/auth/register/begin", func(w http.ResponseWriter, r *http.Request) {
		if !a.browserOrigin(w, r) || !a.rateLimit(w, r) {
			return
		}
		var in struct {
			Invite string `json:"invite"`
		}
		if !decode(w, r, &in) {
			return
		}
		hash := invitationHash(in.Invite)
		inv := a.db.invitationByHash(hash)
		if len(in.Invite) < 32 || inv == nil || !inv.Expires.After(time.Now()) {
			failure(w, 403, "Einladung ungültig oder abgelaufen")
			return
		}
		userID, err := secret()
		if err != nil {
			failure(w, 500, "Zufallsquelle fehlgeschlagen")
			return
		}
		u := &user{ID: userID, Name: inv.Name, CreatedAt: time.Now().UTC(), Credentials: []webauthn.Credential{}, Topics: []string{}}
		creation, sess, err := a.wa.BeginRegistration(u)
		if err != nil {
			failure(w, 500, "WebAuthn Initialisierung fehlgeschlagen")
			return
		}
		id, err := a.auth.putCeremony(ceremony{Kind: "invite", User: u, InviteHash: hash, Session: *sess, Expires: time.Now().Add(ceremonyTTL)})
		if err != nil {
			failure(w, 503, "Zu viele Anfragen")
			return
		}
		reply(w, 200, map[string]any{"ceremonyId": id, "options": creation})
	})
	mux.HandleFunc("POST /api/auth/register/finish", func(w http.ResponseWriter, r *http.Request) {
		if !a.browserOrigin(w, r) || !a.rateLimit(w, r) {
			return
		}
		c, in, ok := a.finishInput(w, r, "invite")
		if !ok {
			return
		}
		credential, err := a.wa.FinishRegistration(c.User, c.Session, authRequest(r, in.Response))
		if err != nil {
			failure(w, 401, "Passkey-Verifikation fehlgeschlagen")
			return
		}
		c.User.Credentials = append(c.User.Credentials, *credential)
		err = a.db.update(func(d *database) error {
			inv := d.Invites[c.InviteHash]
			if inv == nil || !inv.Expires.After(time.Now()) {
				return errors.New("invitation expired or used")
			}
			if d.Users[c.User.ID] != nil {
				return errors.New("duplicate user")
			}
			d.Users[c.User.ID] = c.User
			delete(d.Invites, c.InviteHash)
			return nil
		})
		if err != nil {
			failure(w, 409, "Einladung bereits verwendet oder Speicherung fehlgeschlagen")
			return
		}
		if err = a.setSession(w, c.User.ID); err != nil {
			failure(w, 500, "Sitzung fehlgeschlagen")
			return
		}
		reply(w, 201, map[string]any{"name": c.User.Name})
	})
	mux.HandleFunc("POST /api/auth/login/begin", func(w http.ResponseWriter, r *http.Request) {
		if !a.browserOrigin(w, r) || !a.rateLimit(w, r) {
			return
		}
		assertion, sess, err := a.wa.BeginDiscoverableLogin()
		if err != nil {
			failure(w, 500, "WebAuthn Initialisierung fehlgeschlagen")
			return
		}
		id, err := a.auth.putCeremony(ceremony{Kind: "login", Session: *sess, Expires: time.Now().Add(ceremonyTTL)})
		if err != nil {
			failure(w, 503, "Zu viele Anfragen")
			return
		}
		reply(w, 200, map[string]any{"ceremonyId": id, "options": assertion})
	})
	mux.HandleFunc("POST /api/auth/login/finish", func(w http.ResponseWriter, r *http.Request) {
		if !a.browserOrigin(w, r) || !a.rateLimit(w, r) {
			return
		}
		c, in, ok := a.finishInput(w, r, "login")
		if !ok {
			return
		}
		found, credential, err := a.wa.FinishPasskeyLogin(func(rawID, userHandle []byte) (webauthn.User, error) { return a.db.findPasskey(rawID, userHandle) }, c.Session, authRequest(r, in.Response))
		if err != nil {
			failure(w, 401, "Passkey-Verifikation fehlgeschlagen")
			return
		}
		u, ok := found.(*user)
		if !ok {
			failure(w, 500, "Internes Benutzerformat")
			return
		}
		if err = a.db.putCredential(u.ID, *credential); err != nil {
			failure(w, 500, "Passkey konnte nicht gespeichert werden")
			return
		}
		if err = a.setSession(w, u.ID); err != nil {
			failure(w, 500, "Sitzung fehlgeschlagen")
			return
		}
		reply(w, 200, map[string]any{"name": u.Name})
	})
	mux.HandleFunc("POST /api/auth/logout", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		a.clearSession(w, r)
		reply(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("POST /api/me/passkeys/begin", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !a.rateLimit(w, r) {
			return
		}
		creation, sess, err := a.wa.BeginRegistration(u)
		if err != nil {
			failure(w, 500, "WebAuthn Initialisierung fehlgeschlagen")
			return
		}
		id, err := a.auth.putCeremony(ceremony{Kind: "add-passkey", User: u, Session: *sess, Expires: time.Now().Add(ceremonyTTL)})
		if err != nil {
			failure(w, 503, "Zu viele Anfragen")
			return
		}
		reply(w, 200, map[string]any{"ceremonyId": id, "options": creation})
	}))
	mux.HandleFunc("POST /api/me/passkeys/finish", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		c, in, ok := a.finishInput(w, r, "add-passkey")
		if !ok {
			return
		}
		if c.User.ID != u.ID {
			failure(w, 403, "Andere Sitzung")
			return
		}
		cred, err := a.wa.FinishRegistration(u, c.Session, authRequest(r, in.Response))
		if err != nil {
			failure(w, 401, "Passkey-Verifikation fehlgeschlagen")
			return
		}
		err = a.db.update(func(d *database) error {
			account := d.Users[u.ID]
			if account == nil {
				return errors.New("user missing")
			}
			for _, other := range account.Credentials {
				if constantBytesEqual(other.ID, cred.ID) {
					return errors.New("duplicate credential")
				}
			}
			// A credential must never be assigned to multiple accounts.
			for _, other := range d.Users {
				for _, existing := range other.Credentials {
					if constantBytesEqual(existing.ID, cred.ID) {
						return errors.New("already registered")
					}
				}
			}
			account.Credentials = append(account.Credentials, *cred)
			return nil
		})
		if err != nil {
			failure(w, 409, "Passkey bereits vorhanden oder Speicherung fehlgeschlagen")
			return
		}
		reply(w, 201, map[string]bool{"ok": true})
	}))
}
