package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Cleanup is deliberately opt-in, never executed by the background delivery worker.
// In particular, unread messages and active deliveries are retained by default.
type cleanupOptions struct {
	MessageDays  int    `json:"messageDays"`
	DeliveryDays int    `json:"deliveryDays"`
	DeleteUnread bool   `json:"deleteUnread"`
	Confirm      string `json:"confirm,omitempty"`
}

type cleanupResult struct {
	Messages       int   `json:"messages"`
	UnreadMessages int   `json:"unreadMessages"`
	Deliveries     int   `json:"deliveries"`
	Invites        int   `json:"expiredInvites"`
	Idempotency    int   `json:"expiredIdempotencyKeys"`
	LongTextBytes  int64 `json:"longTextBytes"`
}

func (o cleanupOptions) validate() error {
	if o.MessageDays < 1 || o.MessageDays > 3650 || o.DeliveryDays < 1 || o.DeliveryDays > 3650 {
		return errors.New("Aufbewahrungsfristen müssen zwischen 1 und 3650 Tagen liegen")
	}
	return nil
}

func terminalDelivery(status string) bool {
	return status == "accepted" || status == "confirmed" || status == "failed" || status == "unconfirmed"
}

// Clean a mutable snapshot. Results are identical for a preview and a committed
// transaction given the same snapshot and reference instant.
func cleanupDatabase(d *database, opt cleanupOptions, now time.Time) cleanupResult {
	var result cleanupResult
	msgCutoff := now.AddDate(0, 0, -opt.MessageDays)
	deliveryCutoff := now.AddDate(0, 0, -opt.DeliveryDays)
	active := make(map[string]bool)
	protectedMessageIDs := make(map[string]bool)
	for _, record := range d.Idempotency {
		if !record.CreatedAt.Before(now.Add(-7 * 24 * time.Hour)) {
			protectedMessageIDs[record.Result.MessageID] = true
		}
	}
	for _, job := range d.Deliveries {
		if !terminalDelivery(job.Status) {
			active[job.UserID+"\x00"+job.MessageID] = true
		}
	}
	for uid, messages := range d.Messages {
		kept := make([]inboxMessage, 0, len(messages))
		for _, m := range messages {
			eligible := m.CreatedAt.Before(msgCutoff) && (opt.DeleteUnread || m.ReadAt != nil) && !active[uid+"\x00"+m.ID] && !protectedMessageIDs[m.ID]
			if eligible {
				result.Messages++
				result.LongTextBytes += int64(len(m.LongText))
				if m.ReadAt == nil {
					result.UnreadMessages++
				}
			} else {
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			delete(d.Messages, uid)
		} else {
			d.Messages[uid] = kept
		}
	}
	for id, job := range d.Deliveries {
		if terminalDelivery(job.Status) && job.CreatedAt.Before(deliveryCutoff) {
			delete(d.Deliveries, id)
			result.Deliveries++
		}
	}
	for token, inv := range d.Invites {
		if !inv.Expires.After(now) {
			delete(d.Invites, token)
			result.Invites++
		}
	}
	for key, record := range d.Idempotency {
		// Match the seven-day deduplication window. Never shorten this by UI settings.
		if record.CreatedAt.Before(now.Add(-7 * 24 * time.Hour)) {
			delete(d.Idempotency, key)
			result.Idempotency++
		}
	}
	return result
}

func (s *store) previewCleanup(opt cleanupOptions, now time.Time) (cleanupResult, error) {
	if err := opt.validate(); err != nil {
		return cleanupResult{}, err
	}
	snap := s.snapshot()
	return cleanupDatabase(&snap, opt, now), nil
}
func (s *store) runCleanup(opt cleanupOptions, now time.Time) (cleanupResult, error) {
	if err := opt.validate(); err != nil {
		return cleanupResult{}, err
	}
	var result cleanupResult
	err := s.update(func(db *database) error {
		result = cleanupDatabase(db, opt, now)
		return nil
	})
	return result, err
}

type storageStatistics struct {
	Users            int            `json:"users"`
	Devices          int            `json:"devices"`
	Topics           int            `json:"topics"`
	Subscribers      int            `json:"topicSubscriptions"`
	Messages         int            `json:"messages"`
	UnreadMessages   int            `json:"unreadMessages"`
	LongTextBytes    int64          `json:"longTextBytes"`
	Deliveries       int            `json:"deliveries"`
	DeliveryStatuses map[string]int `json:"deliveryStatuses"`
	Invites          int            `json:"invites"`
	IdempotencyKeys  int            `json:"idempotencyKeys"`
	MainDBBytes      int64          `json:"mainDbBytes"`
	WALBytes         int64          `json:"walBytes"`
	SHMBytes         int64          `json:"shmBytes"`
	TotalDiskBytes   int64          `json:"totalDiskBytes"`
	FreePagesBytes   int64          `json:"freePagesBytes"`
	PageSize         int64          `json:"pageSize"`
	Integrity        string         `json:"integrity"`
}

func fileBytes(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// SQL aggregate queries avoid transferring large message bodies and are
// serialized against in-process updates for internally consistent counts.
func (s *store) storageStats() (storageStatistics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	x := storageStatistics{DeliveryStatuses: map[string]int{}}
	for _, spec := range []struct {
		target *int
		query  string
	}{
		{&x.Users, `SELECT COUNT(*) FROM users`},
		{&x.Devices, `SELECT COUNT(*) FROM devices`},
		{&x.Topics, `SELECT COUNT(*) FROM topics`},
		{&x.Messages, `SELECT COUNT(*) FROM messages`},
		{&x.Deliveries, `SELECT COUNT(*) FROM deliveries`},
		{&x.Invites, `SELECT COUNT(*) FROM invites`},
		{&x.IdempotencyKeys, `SELECT COUNT(*) FROM idempotency`},
	} {
		if err := s.sqlDB.QueryRow(spec.query).Scan(spec.target); err != nil {
			return x, err
		}
	}
	if err := s.sqlDB.QueryRow(`SELECT COALESCE(SUM(length(long_text)),0) FROM messages`).Scan(&x.LongTextBytes); err != nil {
		return x, err
	}
	// Read status is in the per-row JSON payload and the in-process cache.
	for _, messages := range s.db.Messages {
		for _, m := range messages {
			if m.ReadAt == nil {
				x.UnreadMessages++
			}
		}
	}
	for _, u := range s.db.Users {
		x.Subscribers += len(u.Topics)
	}
	statuses, err := s.sqlDB.Query(`SELECT status, COUNT(*) FROM deliveries GROUP BY status`)
	if err != nil {
		return x, err
	}
	for statuses.Next() {
		var status string
		var count int
		if err := statuses.Scan(&status, &count); err != nil {
			statuses.Close()
			return x, err
		}
		x.DeliveryStatuses[status] = count
	}
	err = statuses.Err()
	statuses.Close()
	if err != nil {
		return x, err
	}
	var pages, freePages int64
	if err := s.sqlDB.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		return x, err
	}
	if err := s.sqlDB.QueryRow(`PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return x, err
	}
	if err := s.sqlDB.QueryRow(`PRAGMA page_size`).Scan(&x.PageSize); err != nil {
		return x, err
	}
	x.FreePagesBytes = freePages * x.PageSize
	x.MainDBBytes = fileBytes(s.path)
	x.WALBytes = fileBytes(s.path + "-wal")
	x.SHMBytes = fileBytes(s.path + "-shm")
	x.TotalDiskBytes = x.MainDBBytes + x.WALBytes + x.SHMBytes
	if err := s.sqlDB.QueryRow(`PRAGMA quick_check`).Scan(&x.Integrity); err != nil {
		return x, err
	}
	return x, nil
}

// Compact may need temporary disk space. Do not hold a transaction while
// issuing VACUUM, and serialize against any active send/ACK mutation.
func (s *store) compact() (beforeBytes, afterBytes int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size := func() int64 { return fileBytes(s.path) + fileBytes(s.path+"-wal") + fileBytes(s.path+"-shm") }
	beforeBytes = size()
	var integrity string
	if err = s.sqlDB.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil {
		return
	}
	if integrity != "ok" {
		err = fmt.Errorf("SQLite quick_check: %s", integrity)
		return
	}
	if _, err = s.sqlDB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return
	}
	if _, err = s.sqlDB.Exec(`VACUUM`); err != nil {
		return
	}
	if _, err = s.sqlDB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return
	}
	afterBytes = size()
	return
}

func (a *application) storageAdminRoutes(mux *http.ServeMux) {
	admin := a.requireUser(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !u.isAdmin() {
			failure(w, 403, "Nur für Administratoren")
			return
		}
		switch r.URL.Path {
		case "/api/admin/storage/stats":
			stats, err := a.db.storageStats()
			if err != nil {
				failure(w, 500, "Statistik konnte nicht ermittelt werden")
				return
			}
			reply(w, 200, stats)
		case "/api/admin/storage/preview", "/api/admin/storage/cleanup":
			if r.Method == http.MethodPost && !a.rateLimit(w, r) {
				return
			}
			var opt cleanupOptions
			if !decode(w, r, &opt) {
				return
			}
			if err := opt.validate(); err != nil {
				failure(w, 400, err.Error())
				return
			}
			if r.URL.Path == "/api/admin/storage/cleanup" {
				if opt.Confirm != "LÖSCHEN" {
					failure(w, 400, "Bestätigung LÖSCHEN erforderlich")
					return
				}
				result, err := a.db.runCleanup(opt, time.Now().UTC())
				if err != nil {
					failure(w, 500, "Bereinigung fehlgeschlagen, keine Änderungen gespeichert")
					return
				}
				reply(w, 200, result)
			} else {
				result, err := a.db.previewCleanup(opt, time.Now().UTC())
				if err != nil {
					failure(w, 400, err.Error())
					return
				}
				reply(w, 200, result)
			}
		case "/api/admin/storage/compact":
			if !a.rateLimit(w, r) {
				return
			}
			var req struct {
				Confirm string `json:"confirm"`
			}
			if !decode(w, r, &req) {
				return
			}
			if strings.TrimSpace(req.Confirm) != "OPTIMIEREN" {
				failure(w, 400, "Bestätigung OPTIMIEREN erforderlich")
				return
			}
			before, after, err := a.db.compact()
			if err != nil {
				failure(w, 500, "SQLite-Optimierung fehlgeschlagen: "+err.Error())
				return
			}
			reply(w, 200, map[string]any{"beforeBytes": before, "afterBytes": after})
		default:
			failure(w, 404, "Unbekannte Wartungsaktion")
		}
	})
	mux.HandleFunc("GET /api/admin/storage/stats", admin)
	mux.HandleFunc("POST /api/admin/storage/preview", admin)
	mux.HandleFunc("POST /api/admin/storage/cleanup", admin)
	mux.HandleFunc("POST /api/admin/storage/compact", admin)
}
