package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogsRedactSecretsAndReportAuthorization(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	cookie := signIn(t, a, "alice")
	output.Reset()
	r := httptest.NewRequest("GET", "/api/me?invite=SECRET_INVITE&token=SECRET_QUERY", nil)
	r.Header.Set("Cookie", cookie)
	r.Header.Set("Authorization", "Bearer SECRET_TOKEN")
	r.Header.Set("User-Agent", "Pushie-Test/1.0")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatal(err, output.String())
	}
	if entry["user_id"] != "alice" || entry["auth"] != "session" || entry["route"] != "GET /api/me" || entry["status"] != float64(200) {
		t.Fatal(entry)
	}
	if entry["request_id"] != w.Header().Get("X-Request-ID") {
		t.Fatal("missing correlation")
	}
	if entry["user_agent"] != "Pushie-Test/1.0" {
		t.Fatal("missing browser diagnostic", entry)
	}
	if strings.Contains(output.String(), "SECRET") || strings.Contains(output.String(), cookie) {
		t.Fatal("credential leaked")
	}
	output.Reset()
	r = httptest.NewRequest("DELETE", "/api/me/messages/private-id", nil)
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if !strings.Contains(output.String(), `"result":"unauthenticated"`) || strings.Contains(output.String(), "private-id") {
		t.Fatal(output.String())
	}
}

func TestDiagnosticUserAgentIsBoundedAndPrintable(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("User-Agent", "browser\n\x00"+strings.Repeat("x", 300))
	value := diagnosticUserAgent(r)
	if len(value) != 256 || !strings.HasPrefix(value, "browser??") {
		t.Fatalf("unexpected user agent: %q", value)
	}
}
func TestRequestLoggerPreservesResponseBodyAndStatus(t *testing.T) {
	a := testApp(t)
	handler := a.requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418); w.Write([]byte("test")) }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))
	if w.Code != 418 || w.Body.String() != "test" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestTraceIdentifiersAreBoundedAndNotCredentials(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/me/sync?client_id=browser-123&page_id=page_456&push_device_id=push-789", nil)
	if traceIdentifier(r, "X-Client-ID", "client_id") != "browser-123" {
		t.Fatal("missing websocket trace")
	}
	r.Header.Set("X-Client-ID", strings.Repeat("x", 81))
	if traceIdentifier(r, "X-Client-ID", "client_id") != "" {
		t.Fatal("unbounded trace")
	}
	r.Header.Set("X-Client-ID", "secret.token")
	if traceIdentifier(r, "X-Client-ID", "client_id") != "" {
		t.Fatal("invalid characters")
	}
	r = httptest.NewRequest("GET", "/api/me?client_id=browser-123", nil)
	if traceIdentifier(r, "X-Client-ID", "client_id") != "" {
		t.Fatal("query accepted outside websocket endpoint")
	}
}
