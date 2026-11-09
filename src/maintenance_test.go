package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorageCleanupPreviewCommitAndReload(t *testing.T) {
	a := testApp(t)
	defer a.db.Close()
	addTestUser(t, a, "alice", "admin", []string{"backup"})
	addTestUser(t, a, "bob", "", nil)
	now := time.Now().UTC()
	old := now.Add(-120 * 24 * time.Hour)
	recent := now.Add(-2 * time.Hour)
	if err := a.db.update(func(d *database) error {
		d.Messages["alice"] = []inboxMessage{
			{ID: "read-old", Title: "read", LongText: strings.Repeat("details", 100), CreatedAt: old, ReadAt: &old},
			{ID: "unread-old", Title: "unread", CreatedAt: old},
			{ID: "active-old", Title: "active", CreatedAt: old, ReadAt: &old},
			{ID: "read-recent", Title: "recent", CreatedAt: recent, ReadAt: &recent},
		}
		d.Messages["bob"] = []inboxMessage{{ID: "bob-read-old", CreatedAt: old, ReadAt: &old}}
		d.Deliveries["active"] = &delivery{ID: "active", UserID: "alice", MessageID: "active-old", Status: "retry", CreatedAt: old}
		d.Deliveries["done"] = &delivery{ID: "done", UserID: "alice", MessageID: "read-old", Status: "confirmed", CreatedAt: old}
		d.Deliveries["new-done"] = &delivery{ID: "new-done", UserID: "alice", MessageID: "read-recent", Status: "failed", CreatedAt: recent}
		d.Invites["expired"] = &invitation{Name: "exp", Expires: old}
		d.Invites["valid"] = &invitation{Name: "valid", Expires: now.Add(time.Hour)}
		d.Idempotency["expired"] = idempotencyRecord{CreatedAt: old}
		d.Idempotency["valid"] = idempotencyRecord{CreatedAt: recent}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	opt := cleanupOptions{MessageDays: 90, DeliveryDays: 30}
	preview, err := a.db.previewCleanup(opt, now)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Messages != 2 || preview.UnreadMessages != 0 || preview.Deliveries != 1 || preview.Invites != 1 || preview.Idempotency != 1 || preview.LongTextBytes != int64(len(strings.Repeat("details", 100))) {
		t.Fatalf("wrong preview: %+v", preview)
	}
	if len(a.db.snapshot().Messages["alice"]) != 4 {
		t.Fatal("preview mutated live DB")
	}
	executed, err := a.db.runCleanup(opt, now)
	if err != nil {
		t.Fatal(err)
	}
	if executed != preview {
		t.Fatalf("preview mismatch: %+v vs %+v", preview, executed)
	}
	after := a.db.snapshot()
	if len(after.Messages["alice"]) != 3 || len(after.Messages["bob"]) != 0 || len(after.Deliveries) != 2 || after.Deliveries["active"] == nil || after.Invites["valid"] == nil || after.Idempotency["valid"].CreatedAt.IsZero() {
		t.Fatal("cleanup deleted protected state or failed to delete old records")
	}
	stats, err := a.db.storageStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Users != 2 || stats.Messages != 3 || stats.UnreadMessages != 1 || stats.Deliveries != 2 || stats.Integrity != "ok" {
		t.Fatalf("wrong stats: %+v", stats)
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.snapshot().Messages["alice"]) != 3 || len(reopened.snapshot().Deliveries) != 2 {
		t.Fatal("cleanup not persisted across restart")
	}
	// An explicit opt-in may delete unread, but a running job still blocks old inbox removal.
	opt.DeleteUnread = true
	more, err := a.db.runCleanup(opt, now)
	if err != nil {
		t.Fatal(err)
	}
	if more.Messages != 1 || more.UnreadMessages != 1 {
		t.Fatalf("unexpected unread cleanup: %+v", more)
	}
}

func TestStorageAdminAuthorizationValidationAndVacuum(t *testing.T) {
	a := testApp(t)
	defer a.db.Close()
	addTestUser(t, a, "admin", "admin", nil)
	addTestUser(t, a, "normal", "", nil)
	adminCookie := signIn(t, a, "admin")
	normalCookie := signIn(t, a, "normal")
	h := a.routes()
	for _, path := range []string{"/api/admin/storage/stats", "/api/admin/storage/preview", "/api/admin/storage/cleanup", "/api/admin/storage/compact"} {
		method := "POST"
		if path == "/api/admin/storage/stats" {
			method = "GET"
		}
		if r := request(t, h, method, path, map[string]any{}, a.cfg.origin, "", normalCookie); r.Code != 403 {
			t.Errorf("non-admin %s returned %d", path, r.Code)
		}
		if r := request(t, h, method, path, map[string]any{}, a.cfg.origin, "", ""); r.Code != 401 {
			t.Errorf("anonymous %s returned %d", path, r.Code)
		}
	}
	opt := cleanupOptions{MessageDays: 90, DeliveryDays: 30}
	if r := request(t, h, "POST", "/api/admin/storage/preview", opt, "", "", adminCookie); r.Code != 403 {
		t.Fatalf("CSRF: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/admin/storage/preview", cleanupOptions{MessageDays: -10, DeliveryDays: 30}, a.cfg.origin, "", adminCookie); r.Code != 400 {
		t.Fatalf("bad days: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/admin/storage/cleanup", opt, a.cfg.origin, "", adminCookie); r.Code != 400 {
		t.Fatalf("missing confirmation: %d", r.Code)
	}
	if r := request(t, h, "POST", "/api/admin/storage/preview", opt, a.cfg.origin, "", adminCookie); r.Code != 200 {
		t.Fatalf("preview failed: %d %s", r.Code, r.Body.String())
	}
	if r := request(t, h, "GET", "/api/admin/storage/stats", nil, "", "", adminCookie); r.Code != 200 {
		t.Fatalf("stats failed: %d %s", r.Code, r.Body.String())
	} else {
		var stats storageStatistics
		if err := json.Unmarshal(r.Body.Bytes(), &stats); err != nil || stats.Users != 2 {
			t.Fatalf("bad stats: %+v %v", stats, err)
		}
	}
	if r := request(t, h, "POST", "/api/admin/storage/cleanup", cleanupOptions{MessageDays: 90, DeliveryDays: 30, Confirm: "LÖSCHEN"}, a.cfg.origin, "", adminCookie); r.Code != 200 {
		t.Fatalf("cleanup failed: %d %s", r.Code, r.Body.String())
	}
	if r := request(t, h, "POST", "/api/admin/storage/compact", map[string]string{"confirm": "OPTIMIEREN"}, a.cfg.origin, "", adminCookie); r.Code != 200 {
		t.Fatalf("vacuum failed: %d %s", r.Code, r.Body.String())
	}
	if r := request(t, h, "GET", "/api/admin/storage/stats", nil, "", "", adminCookie); r.Code != 200 {
		t.Fatalf("stats after vacuum: %d", r.Code)
	}
}

func TestMaintenanceOnEmptySQLite(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "empty.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, after, err := s.compact()
	if err != nil {
		t.Fatal(err)
	}
	if before < 0 || after <= 0 {
		t.Fatalf("invalid file size: %d -> %d", before, after)
	}
	stats, err := s.storageStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Integrity != "ok" {
		t.Fatalf("unexpected integrity: %q", stats.Integrity)
	}
}
