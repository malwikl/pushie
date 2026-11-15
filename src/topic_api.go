package main

import (
	"net/http"
	"sort"
)

// Topic integration endpoints never return account or device information.
func (a *application) topicAPIRoutes(mux *http.ServeMux) {
	authorize := func(w http.ResponseWriter, r *http.Request) bool {
		if !authorized(r, a.cfg.sendToken) {
			if checked, ok := a.authorizeSend(w, r); ok {
				_ = checked
				reply(w, 403, map[string]string{"error": "topic_permission_required"})
			}
			return false
		}
		if !a.rateLimit(w, r) {
			return false
		}
		if o := r.Header.Get("Origin"); o != "" && o != a.cfg.origin {
			reply(w, 403, map[string]string{"error": "invalid_origin"})
			return false
		}
		markAuthorization(r, "", "send_token")
		return true
	}
	mux.HandleFunc("GET /api/topics", func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r) {
			return
		}
		topics := make([]topic, 0)
		for _, t := range a.db.snapshot().Topics {
			topics = append(topics, *t)
		}
		sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
		reply(w, 200, map[string]any{"topics": topics})
	})
	mux.HandleFunc("POST /api/verify", func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r) {
			return
		}
		var req struct {
			Topic string `json:"topic"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.Topic != "" {
			if !topicRE.MatchString(req.Topic) {
				reply(w, 400, map[string]string{"error": "invalid_topic"})
				return
			}
			if a.db.snapshot().Topics[req.Topic] == nil {
				reply(w, 404, map[string]string{"error": "topic_not_found"})
				return
			}
		}
		result := map[string]any{"ok": true, "apiVersion": 1, "capabilities": []string{"topics:list", "topics:send", "verify"}}
		if req.Topic != "" {
			result["topic"] = req.Topic
		}
		reply(w, 200, result)
	})
}
