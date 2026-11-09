package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type requestLogKey struct{}
type requestLogInfo struct {
	id, userID, auth               string
	clientID, pageID, pushDeviceID string
	appVersion                     string
}

var requestSequence atomic.Uint64
var traceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func diagnosticUserAgent(r *http.Request) string {
	value := r.UserAgent()
	if len(value) > 256 {
		value = value[:256]
	}
	return strings.Map(func(c rune) rune {
		if c < 32 || c > 126 {
			return '?'
		}
		return c
	}, value)
}

func traceIdentifier(r *http.Request, header, query string) string {
	value := r.Header.Get(header)
	if value == "" && r.URL.Path == "/api/me/sync" {
		value = r.URL.Query().Get(query)
	}
	if !traceIDPattern.MatchString(value) {
		return ""
	}
	return value
}

func markAuthorization(r *http.Request, userID, method string) {
	if info, ok := r.Context().Value(requestLogKey{}).(*requestLogInfo); ok {
		info.userID = userID
		info.auth = method
	}
}
func requestFields(r *http.Request) []any {
	fields := []any{}
	if info, ok := r.Context().Value(requestLogKey{}).(*requestLogInfo); ok {
		fields = append(fields, "request_id", info.id, "user_id", info.userID, "auth", info.auth, "client_id", info.clientID, "page_id", info.pageID, "push_device_id", info.pushDeviceID, "app_version", info.appVersion)
	}
	return fields
}

// Do not log URLs, query strings, headers, request/response bodies or tokens.
// Route templates identify actions without leaking invitation or endpoint data.
func (a *application) requestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		info := &requestLogInfo{id: strconv.FormatInt(started.UnixMilli(), 36) + "-" + strconv.FormatUint(requestSequence.Add(1), 36), auth: "none",
			clientID: traceIdentifier(r, "X-Client-ID", "client_id"), pageID: traceIdentifier(r, "X-Page-ID", "page_id"), pushDeviceID: traceIdentifier(r, "X-Push-Device-ID", "push_device_id"), appVersion: traceIdentifier(r, "X-App-Version", "app_version")}
		r = r.WithContext(context.WithValue(r.Context(), requestLogKey{}, info))
		w.Header().Set("X-Request-ID", info.id)
		response := &loggedResponse{ResponseWriter: w}
		defer func() {
			if response.status == 0 {
				response.status = 200
			}
			if r.URL.Path == "/healthz" && response.status < 400 {
				return
			}
			if (r.Pattern == "/" || r.Pattern == "GET /{$}" || r.Pattern == "GET /index.html" || r.Pattern == "GET /sw.js") && response.status < 400 {
				return
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			result := "success"
			if response.status >= 400 {
				result = "error"
			}
			switch response.status {
			case 401:
				result = "unauthenticated"
			case 403:
				result = "forbidden"
			case 429:
				result = "rate_limited"
			}
			fields := append(requestFields(r), "method", r.Method, "route", route, "status", response.status, "result", result, "duration_ms", time.Since(started).Milliseconds(), "bytes", response.bytes, "client_ip", a.clientIP(r), "user_agent", diagnosticUserAgent(r))
			event := "http.request"
			if r.Method != "GET" && r.Method != "HEAD" {
				event = "action"
			}
			level := slog.LevelInfo
			if response.status >= 400 {
				level = slog.LevelWarn
			}
			if response.status >= 500 {
				level = slog.LevelError
			}
			slog.Log(r.Context(), level, event, fields...)
		}()
		next.ServeHTTP(response, r)
	})
}

type loggedResponse struct {
	http.ResponseWriter
	status, bytes int
}

func (w *loggedResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *loggedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}
func (w *loggedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *loggedResponse) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *loggedResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("WebSocket hijacking unsupported")
	}
	conn, rw, err := hj.Hijack()
	if err == nil {
		w.status = 101
	}
	return conn, rw, err
}
