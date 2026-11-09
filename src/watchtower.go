package main

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

// watchtowerHandler accepts Shoutrrr's generic JSON template. Authentication,
// recipient restrictions, idempotency and durable delivery use the send pipeline.
func (a *application) watchtowerHandler(w http.ResponseWriter, r *http.Request) {
	r, ok := a.authorizeWatchtower(w, r)
	if !ok {
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != a.cfg.origin {
		failure(w, 403, "Origin nicht erlaubt")
		return
	}
	var payload struct {
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	if !decode(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.Message) == "" || !utf8.ValidString(payload.Message) || len(payload.Message) > 256*1024 {
		failure(w, 400, "Nachricht ungültig oder zu lang")
		return
	}
	q := r.URL.Query()
	for _, key := range []string{"topic", "userId", "deviceId"} {
		if len(q[key]) > 1 {
			failure(w, 400, "Empfänger mehrfach angegeben")
			return
		}
	}
	req := sendRequest{notification: notification{Title: payload.Title, Body: payload.Message, Topic: q.Get("topic")}, UserID: q.Get("userId"), DeviceID: q.Get("deviceId")}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = "Watchtower"
	}
	_, personal := r.Context().Value(personalSenderContext{}).(personalSender)
	if !personal && req.Topic == "" && req.UserID == "" && req.DeviceID == "" {
		req.Topic = "automation"
	}
	req.LongText = payload.Message
	if len(req.Body) > 2000 {
		preview := req.Body[:1800]
		for !utf8.ValidString(preview) {
			preview = preview[:len(preview)-1]
		}
		req.Body = preview + "\n… (vollständiger Bericht im Posteingang)"
	}
	a.dispatchNotification(w, r, req)
}
