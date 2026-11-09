// pushiectl sends notifications to the pushie server using the token API.
// This binary only uses Go's standard library; it can run on another machine.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	fs := flag.NewFlagSet("pushiectl", flag.ExitOnError)
	server := fs.String("url", os.Getenv("PUSH_BASE_URL"), "HTTPS base URL (or PUSH_BASE_URL)")
	token := fs.String("token", os.Getenv("PUSH_API_TOKEN"), "API token (prefer PUSH_API_TOKEN environment)")
	topic := fs.String("topic", "", "target topic")
	user := fs.String("user", "", "target user ID")
	device := fs.String("device", "", "target device ID")
	title := fs.String("title", "Push", "notification title")
	body := fs.String("body", "", "notification body")
	stdin := fs.Bool("stdin", false, "read body from stdin")
	tag := fs.String("tag", "", "notification tag")
	longTextFile := fs.String("longtext-file", "", "file containing full detail text (max 256 KiB)")
	statusID := fs.String("status", "", "query delivery status of a previously sent messageId")
	key := fs.String("key", "", "optional idempotency key: reuse on retry after a timeout")
	fs.Parse(os.Args[1:])
	if *stdin {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 2001))
		if err != nil {
			fatal("stdin: %v", err)
		}
		if len(b) > 2000 {
			fatal("message too long")
		}
		*body = string(b)
	}
	u, err := url.Parse(*server)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		fatal("-url must be an HTTPS origin, e.g. https://push.example.com")
	}
	if *token == "" {
		fatal("set PUSH_API_TOKEN (a personal API key or the server SEND_TOKEN)")
	}
	if *statusID == "" && strings.TrimSpace(*body) == "" {
		fatal("set -body or -stdin")
	}
	payload := map[string]string{}
	if *statusID == "" {
		n := 0
		for _, v := range []string{*topic, *user, *device} {
			if v != "" {
				n++
			}
		}
		if n > 1 {
			fatal("specify at most one of -topic, -user or -device; personal keys default to your account")
		}
		payload = map[string]string{"title": *title, "body": *body}
		if *tag != "" {
			payload["tag"] = *tag
		}
		if *longTextFile != "" {
			longText, err := os.ReadFile(*longTextFile)
			if err != nil {
				fatal("read long text: %v", err)
			}
			if len(longText) > 256*1024 {
				fatal("long text too large (max 256 KiB)")
			}
			payload["longText"] = string(longText)
		}
		if *topic != "" {
			payload["topic"] = *topic
		}
		if *user != "" {
			payload["userId"] = *user
		}
		if *device != "" {
			payload["deviceId"] = *device
		}
	}
	if *statusID != "" && (strings.ContainsAny(*statusID, "/?#") || len(*statusID) > 128 || len(*statusID) < 10) {
		fatal("invalid messageId")
	}
	b, _ := json.Marshal(payload)
	method, path := http.MethodPost, "/api/send"
	var payloadBody io.Reader = bytes.NewReader(b)
	if *statusID != "" {
		method = http.MethodGet
		path = "/api/send/status/" + url.PathEscape(*statusID)
		payloadBody = nil
	}
	req, err := http.NewRequest(method, *server+path, payloadBody)
	if err != nil {
		fatal("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+*token)
	req.Header.Set("Content-Type", "application/json")
	if *statusID == "" && *key != "" {
		req.Header.Set("Idempotency-Key", *key)
	}
	client := http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		fatal("send: %v", err)
	}
	defer res.Body.Close()
	result, err := io.ReadAll(io.LimitReader(res.Body, 8192))
	if err != nil {
		fatal("response: %v", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		fatal("server returned %s: %s", res.Status, strings.TrimSpace(string(result)))
	}
	fmt.Println(string(result))
}
func fatal(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }
