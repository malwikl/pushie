package main

import "net/http"

type messageCleanupRequest struct {
	Confirm string   `json:"confirm"`
	IDs     []string `json:"ids"`
}

func validMessageSelection(ids []string) bool {
	if len(ids) == 0 || len(ids) > maxInboxMessages {
		return false
	}
	for _, id := range ids {
		if len(id) == 0 || len(id) > 128 {
			return false
		}
	}
	return true
}

func (a *application) messageCleanupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/me/messages/cleanup", a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !a.rateLimit(w, r) {
			return
		}
		var req messageCleanupRequest
		if !decode(w, r, &req) {
			return
		}
		if req.Confirm != "LÖSCHEN" {
			failure(w, 400, "Bestätigung erforderlich")
			return
		}
		if !validMessageSelection(req.IDs) {
			failure(w, 400, "Nachrichtenauswahl ungültig")
			return
		}
		selected := make(map[string]bool, len(req.IDs))
		for _, id := range req.IDs {
			selected[id] = true
		}
		count := 0
		if err := a.db.update(func(db *database) error {
			kept := make([]inboxMessage, 0, len(db.Messages[u.ID]))
			for _, message := range db.Messages[u.ID] {
				if !selected[message.ID] {
					kept = append(kept, message)
					continue
				}
				if message.SenderUserID == u.ID {
					hideSentMessages(db, u.ID, message.ID)
				}
				count++
			}
			db.Messages[u.ID] = kept
			return nil
		}); err != nil {
			failure(w, 500, "Speichern fehlgeschlagen")
			return
		}
		reply(w, 200, map[string]int{"deleted": count})
	}))
}
