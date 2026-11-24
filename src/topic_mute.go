package main

import (
	"errors"
	"maps"
	"net/http"
	"time"
	_ "time/tzdata" // Support browser time zones in minimal containers.
)

func (u *user) topicMuted(name string, now time.Time) bool {
	return u != nil && name != "" && u.TopicMutes[name].After(now)
}

func (u *user) activeTopicMutes(now time.Time) map[string]time.Time {
	active := make(map[string]time.Time)
	for name, until := range u.TopicMutes {
		if until.After(now) {
			active[name] = until
		}
	}
	return active
}

func topicMuteUntil(now time.Time, duration, zone string) (time.Time, error) {
	switch duration {
	case "unmute":
		return time.Time{}, nil
	case "15m":
		return now.Add(15 * time.Minute), nil
	case "30m":
		return now.Add(30 * time.Minute), nil
	case "60m":
		return now.Add(time.Hour), nil
	case "4h":
		return now.Add(4 * time.Hour), nil
	case "tomorrow":
		if zone != "" && zone != "Local" {
			if location, err := time.LoadLocation(zone); err == nil {
				local := now.In(location)
				return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location).UTC(), nil
			}
		}
	}
	return time.Time{}, errors.New("invalid topic mute duration or time zone")
}

func (a *application) updateTopicMute(w http.ResponseWriter, r *http.Request, u *user) {
	if !a.rateLimit(w, r) {
		return
	}
	name := r.PathValue("topic")
	if !topicRE.MatchString(name) {
		failure(w, 400, "Ungültiges Topic")
		return
	}
	var req struct {
		Duration string `json:"duration"`
		TimeZone string `json:"timeZone"`
	}
	if !decode(w, r, &req) {
		return
	}
	missingTopic := errors.New("missing topic subscription")
	invalidDuration := errors.New("invalid mute duration")
	var mutedUntil *time.Time
	err := a.db.update(func(db *database) error {
		account := db.Users[u.ID]
		if account == nil || db.Topics[name] == nil || !hasString(account.Topics, name) {
			return missingTopic
		}
		now := time.Now().UTC()
		until, err := topicMuteUntil(now, req.Duration, req.TimeZone)
		if err != nil {
			return invalidDuration
		}
		maps.DeleteFunc(account.TopicMutes, func(_ string, expires time.Time) bool { return !expires.After(now) })
		if until.IsZero() {
			delete(account.TopicMutes, name)
			return nil
		}
		if account.TopicMutes == nil {
			account.TopicMutes = make(map[string]time.Time)
		}
		account.TopicMutes[name] = until
		mutedUntil = &until
		for _, job := range db.Deliveries {
			if job.UserID == u.ID && job.Content.Topic == name && (job.Status == "pending" || job.Status == "retry") {
				muteDelivery(job)
			}
		}
		return nil
	})
	switch err {
	case missingTopic:
		failure(w, 404, "Topic nicht gefunden")
	case invalidDuration:
		failure(w, 400, "Ungültige Stummschaltung")
	case nil:
		reply(w, 200, map[string]*time.Time{"mutedUntil": mutedUntil})
	default:
		failure(w, 500, "Speichern fehlgeschlagen")
	}
}
