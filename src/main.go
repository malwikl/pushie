package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

//go:embed static/*
var assets embed.FS

type config struct {
	publicKey, privateKey, subject, sendToken, adminToken, bootstrapOTP, origin, rpID, addr, dbPath string
	secureCookies                                                                                   bool
	trustedProxies                                                                                  []netip.Prefix
}
type application struct {
	cfg      config
	db       *store
	auth     *authState
	wa       *webauthn.WebAuthn
	pushSend func(context.Context, []byte, *webpush.Subscription, *webpush.Options) (*http.Response, error)
}

func env(name string) string { return strings.TrimSpace(os.Getenv(name)) }
func secret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func authorized(r *http.Request, token string) bool {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if token == "" || !strings.HasPrefix(h, prefix) {
		return false
	}
	p := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	return subtle.ConstantTimeCompare([]byte(p), []byte(token)) == 1
}
func constantBytesEqual(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
func reply(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
func failure(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	maxBytes := int64(32 * 1024)
	if r.URL.Path == "/api/send" || r.URL.Path == "/api/admin/send" || r.URL.Path == "/api/integrations/watchtower" {
		maxBytes = 2 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		failure(w, 400, "Ungültiges JSON")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		failure(w, 400, "Nur ein JSON-Objekt erlaubt")
		return false
	}
	return true
}

// State-changing browser requests MUST provide the configured origin (not a proxy-derived Host).
func (a *application) browserOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != a.cfg.origin {
		failure(w, 403, "Origin nicht erlaubt")
		return false
	}
	return true
}
func validPushEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Fragment != "" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "fcm.googleapis.com" || h == "updates.push.services.mozilla.com" || h == "push.services.mozilla.com" || h == "web.push.apple.com" || strings.HasSuffix(h, ".push.apple.com")
}
func readConfig() (config, error) {
	c := config{publicKey: env("VAPID_PUBLIC_KEY"), privateKey: env("VAPID_PRIVATE_KEY"), subject: env("VAPID_SUBJECT"), sendToken: env("SEND_TOKEN"), adminToken: env("ADMIN_TOKEN"), bootstrapOTP: env("BOOTSTRAP_OTP"), origin: env("ORIGIN"), rpID: "", addr: env("ADDR"), dbPath: env("DB_PATH")}
	if c.addr == "" {
		c.addr = "127.0.0.1:8099"
	}
	if c.dbPath == "" {
		c.dbPath = "data/webpush.sqlite"
	}
	if c.publicKey == "" || c.privateKey == "" || c.sendToken == "" || c.adminToken == "" || c.origin == "" {
		return c, fmt.Errorf("VAPID keys, SEND_TOKEN, ADMIN_TOKEN and ORIGIN are required")
	}
	if !validVAPIDPair(c.publicKey, c.privateKey) {
		return c, fmt.Errorf("invalid or mismatched VAPID key pair")
	}
	if len(c.sendToken) < 32 || len(c.adminToken) < 32 || c.sendToken == c.adminToken || strings.Contains(c.sendToken, "replace") || strings.Contains(c.adminToken, "replace") {
		return c, fmt.Errorf("use two distinct random tokens at least 32 characters long")
	}
	if c.bootstrapOTP != "" && (len(c.bootstrapOTP) < 20 || len(c.bootstrapOTP) > 256 || strings.Contains(c.bootstrapOTP, "replace") || c.bootstrapOTP == c.adminToken || c.bootstrapOTP == c.sendToken) {
		return c, fmt.Errorf("BOOTSTRAP_OTP must be a distinct random secret (20-256 characters)")
	}
	u, err := url.Parse(c.origin)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(c.origin, "#") {
		return c, fmt.Errorf("ORIGIN must be a public origin without credentials, path, query or fragment")
	}
	c.rpID = u.Hostname()
	if c.subject == "" {
		c.subject = c.origin
	}
	if !strings.HasPrefix(c.subject, "mailto:") && !strings.HasPrefix(c.subject, "https://") {
		return c, fmt.Errorf("invalid VAPID_SUBJECT (set a mailto: or HTTPS contact for local HTTP development)")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return c, fmt.Errorf("ORIGIN must be HTTPS (localhost allowed for development)")
	}
	for _, entry := range strings.Split(env("TRUSTED_PROXIES"), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return c, fmt.Errorf("invalid TRUSTED_PROXIES CIDR %q", entry)
		}
		c.trustedProxies = append(c.trustedProxies, prefix)
	}
	c.secureCookies = u.Scheme == "https"
	return c, nil
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildVersion)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "genkeys" {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("VAPID_PUBLIC_KEY=%s\nVAPID_PRIVATE_KEY=%s\n", pub, priv)
		return
	}
	setup, err := printSetupValues(os.Stdout)
	if err != nil {
		log.Fatal(err)
	}
	if setup {
		os.Exit(1)
	}
	c, err := readConfig()
	if err != nil {
		log.Fatal(err)
	}
	release, err := lockStore(c.dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer release()
	db, err := openStore(c.dbPath)
	if err != nil {
		log.Fatal(err)
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID: c.rpID, RPDisplayName: "pushie", RPOrigins: []string{c.origin},
		AttestationPreference:  protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired, ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired()},
		Timeouts:               webauthn.TimeoutsConfig{Login: webauthn.TimeoutConfig{Enforce: true}, Registration: webauthn.TimeoutConfig{Enforce: true}},
	})
	if err != nil {
		log.Fatal(err)
	}
	a := &application{cfg: c, db: db, auth: newAuthState(), wa: wa}
	defer db.sqlDB.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := a.startDeliveryWorker(ctx)
	server := &http.Server{Addr: c.addr, Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 50 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	slog.Info("server.started", "address", c.addr, "origin", c.origin, "version", buildVersion)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		slog.Info("server.stopping")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
			_ = server.Close()
		}
	}()
	serveErr := server.ListenAndServe()
	stop()
	<-workerDone
	<-shutdownDone
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		_ = db.sqlDB.Close()
		log.Fatal(serveErr)
	}
}
func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]string{"version": buildVersion})
	})
	index := serveVersionedAsset("index.html", "text/html; charset=utf-8")
	mux.HandleFunc("GET /{$}", index)
	mux.HandleFunc("GET /index.html", index)
	mux.HandleFunc("GET /sw.js", serveVersionedAsset("sw.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"vapidPublicKey": a.cfg.publicKey, "bootstrapAvailable": a.db.bootstrapAvailable(a.cfg.bootstrapOTP)})
	})
	mux.HandleFunc("GET /api/me/sync", a.requireUser(a.syncSocket))
	a.authRoutes(mux)
	a.userRoutes(mux)
	a.adminRoutes(mux)
	a.storageAdminRoutes(mux)
	mux.HandleFunc("POST /api/send", a.sendHandler)
	mux.HandleFunc("POST /api/integrations/watchtower", a.watchtowerHandler)
	mux.HandleFunc("GET /api/send/status/{id}", a.sendStatusHandler)
	mux.HandleFunc("POST /api/push/ack", a.acknowledgeDelivery)
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) { r.URL.Path = "/"; mux.ServeHTTP(w, r) })
	dir, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(dir)))
	return a.requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-App-Version", buildVersion)
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self' "+strings.Replace(strings.Replace(a.cfg.origin, "https://", "wss://", 1), "http://", "ws://", 1)+"; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.URL.Path == "/" || r.URL.Path == "/app.js" || r.URL.Path == "/i18n.js" || r.URL.Path == "/theme.js" || r.URL.Path == "/sw.js" || r.URL.Path == "/pull-refresh.js" || r.URL.Path == "/style.css" || r.URL.Path == "/sync.js" || r.URL.Path == "/trace.js" || r.URL.Path == "/version.js" {
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	}))
}
