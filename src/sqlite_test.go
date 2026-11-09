package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteMigratesLegacyJSONWithoutDeletingIt(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "webpush.json")
	sqlitePath := filepath.Join(dir, "webpush.sqlite")
	old := database{
		Users:         map[string]*user{"alex": {ID: "alex", Name: "Alex", Role: "admin", Topics: []string{"backup"}}},
		Devices:       map[string]*device{"phone": {ID: "phone", UserID: "alex", Name: "iPhone", Subscription: dummySubscription("https://web.push.apple.com/a")}},
		Topics:        map[string]*topic{"backup": {Name: "backup", Description: "Backups"}},
		Messages:      map[string][]inboxMessage{"alex": {{ID: "msg1", Title: "Test", Body: "Kurz", LongText: strings.Repeat("Ausführlicher Bericht\n", 2500), CreatedAt: time.Now().UTC()}}},
		Deliveries:    map[string]*delivery{"job1": {ID: "job1", MessageID: "msg1", UserID: "alex", DeviceID: "phone", Status: "retry", NextAttemptAt: time.Now().UTC().Add(5 * time.Minute)}},
		BootstrapUsed: true,
	}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.snapshot()
	if !snap.BootstrapUsed || snap.Users["alex"].Role != "admin" || len(snap.Devices) != 1 || len(snap.Deliveries) != 1 || len(snap.Messages["alex"][0].LongText) < 5000 {
		t.Fatalf("legacy import incomplete: users=%d devices=%d deliveries=%d longText=%d", len(snap.Users), len(snap.Devices), len(snap.Deliveries), len(snap.Messages["alex"][0].LongText))
	}
	var storedLongText string
	if err := s.sqlDB.QueryRow("SELECT long_text FROM messages WHERE user_id=? AND id=?", "alex", "msg1").Scan(&storedLongText); err != nil {
		t.Fatal(err)
	}
	if storedLongText != old.Messages["alex"][0].LongText {
		t.Fatal("long text not stored in SQLite column")
	}
	if err := s.update(func(d *database) error {
		x := d.Messages["alex"]
		now := time.Now().UTC()
		x[0].ReadAt = &now
		d.Messages["alex"] = x
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after := reopened.snapshot()
	if after.Messages["alex"][0].ReadAt == nil || after.Messages["alex"][0].LongText != storedLongText || after.Deliveries["job1"].Status != "retry" {
		t.Fatal("SQLite restart lost state")
	}
	original, err := os.ReadFile(legacyPath)
	if err != nil || string(original) != string(raw) {
		t.Fatal("legacy JSON was modified")
	}
}

func TestSQLiteRollsBackCallbackAndConstraintFailure(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "webpush.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.update(func(d *database) error { d.Users["ok"] = &user{ID: "ok", Name: "OK"}; return nil }); err != nil {
		t.Fatal(err)
	}
	err = s.update(func(d *database) error { d.Users["oops"] = &user{ID: "oops"}; return os.ErrPermission })
	if err == nil || s.snapshot().Users["oops"] != nil {
		t.Fatal("callback rollback failed")
	}
	err = s.update(func(d *database) error { d.Devices["orphan"] = &device{ID: "orphan", UserID: "missing"}; return nil })
	if err == nil || s.snapshot().Devices["orphan"] != nil {
		t.Fatal("foreign key violation was not rolled back")
	}
	var count int
	if err := s.sqlDB.QueryRow("SELECT count(*) FROM devices WHERE id='orphan'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("bad DB state: %d, %v", count, err)
	}
	if err := s.sqlDB.QueryRow("PRAGMA integrity_check").Scan(new(string)); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRejectsOldJSONInDatabasePath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "webpush.json")
	contents := []byte(`{"users":{"x":{}},"topics":{}}`)
	if err := os.WriteFile(p, contents, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(p)
	if err == nil {
		_ = s.Close()
		t.Fatal("legacy JSON accepted as SQLite")
	}
	if !strings.Contains(err.Error(), "not SQLite") {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != string(contents) {
		t.Fatal("old file modified")
	}
}

func TestLongTextSavedInInboxButNotInProviderPayload(t *testing.T) {
	a := testApp(t)
	defer a.db.Close()
	addTestUser(t, a, "alex", "admin", []string{"backup"})
	if err := a.db.update(func(d *database) error {
		d.Devices["dev"] = &device{ID: "dev", UserID: "alex", Subscription: dummySubscription("https://web.push.apple.com/a")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	longText := strings.Repeat("Details here.\n", 10000)
	response := request(t, a.routes(), "POST", "/api/send", map[string]string{"topic": "backup", "title": "Backup", "body": "Kurz", "longText": longText}, "", a.cfg.sendToken, "")
	if response.Code != 200 {
		t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
	}
	snap := a.db.snapshot()
	if len(snap.Messages["alex"]) != 1 || snap.Messages["alex"][0].LongText != longText {
		t.Fatal("long text was not stored")
	}
	for _, d := range snap.Deliveries {
		if d.Content.LongText != "" {
			t.Fatal("long text must not be sent as Web Push payload")
		}
	}
}
