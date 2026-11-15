package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestTopicIntegrationAPI(t *testing.T) {
	a := testApp(t)
	mux := http.NewServeMux()
	a.topicAPIRoutes(mux)
	before := a.db.snapshot()
	catalog := request(t, mux, "GET", "/api/topics", nil, "", a.cfg.sendToken, "")
	var result struct {
		Topics []topic `json:"topics"`
	}
	if err := json.Unmarshal(catalog.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for i, topic := range result.Topics {
		if i > 0 && result.Topics[i-1].Name >= topic.Name {
			t.Fatal("topics not sorted")
		}
		w := request(t, mux, "POST", "/api/verify", map[string]string{"topic": topic.Name}, "", a.cfg.sendToken, "")
		if w.Code != 200 {
			t.Fatalf("known topic rejected: %d", w.Code)
		}
	}
	if w := request(t, mux, "GET", "/api/topics", nil, "https://evil.example", a.cfg.sendToken, ""); w.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	for _, tc := range []struct {
		method, path, token string
		body                any
		status              int
	}{
		{"GET", "/api/topics", a.cfg.sendToken, nil, 200},
		{"POST", "/api/verify", a.cfg.sendToken, map[string]any{}, 200},
		{"POST", "/api/verify", a.cfg.sendToken, map[string]any{"topic": "missing"}, 404},
		{"POST", "/api/verify", a.cfg.sendToken, map[string]any{"topic": "INVALID"}, 400},
		{"POST", "/api/verify", a.cfg.sendToken, map[string]any{"broadcast": true}, 400},
		{"POST", "/api/verify", a.cfg.adminToken, map[string]any{}, 401},
		{"GET", "/api/topics", "", nil, 401},
	} {
		w := request(t, mux, tc.method, tc.path, tc.body, "", tc.token, "")
		if w.Code != tc.status {
			t.Fatalf("%s %s: got %d want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
	if !reflect.DeepEqual(before, a.db.snapshot()) {
		t.Fatal("verification changed database")
	}
}
