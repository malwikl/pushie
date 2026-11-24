package main

import "net/http"

type notificationCleanupRequest struct {
	Confirm string   `json:"confirm"`
	IDs     []string `json:"ids"`
}

func validNotificationSelection(ids []string) bool {
	if len(ids) == 0 || len(ids) > maxInboxNotifications {
		return false
	}
	for _, id := range ids {
		if len(id) == 0 || len(id) > 128 {
			return false
		}
	}
	return true
}

func (a *application) notificationCleanupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/me/notifications/cleanup", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !a.rateLimit(w, r) {
			return
		}
		var req notificationCleanupRequest
		if !decode(w, r, &req) {
			return
		}
		if req.Confirm != "LÖSCHEN" {
			failure(w, 400, "Bestätigung erforderlich")
			return
		}
		if !validNotificationSelection(req.IDs) {
			failure(w, 400, "Mitteilungsauswahl ungültig")
			return
		}
		selected := make(map[string]bool, len(req.IDs))
		for _, id := range req.IDs {
			selected[id] = true
		}
		count := 0
		if err := a.db.update(func(db *database) error {
			kept := make([]inboxNotification, 0, len(db.Notifications[u.ID]))
			for _, notification := range db.Notifications[u.ID] {
				if !selected[notification.ID] {
					kept = append(kept, notification)
					continue
				}
				if notification.SenderUserID == u.ID {
					hideSentNotifications(db, u.ID, notification.ID)
				}
				count++
			}
			db.Notifications[u.ID] = kept
			return nil
		}); err != nil {
			failure(w, 500, "Speichern fehlgeschlagen")
			return
		}
		reply(w, 200, map[string]int{"deleted": count})
	}))
}
