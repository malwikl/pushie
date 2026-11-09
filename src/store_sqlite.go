package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// The application keeps a read cache for its existing API. All authoritative
// persisted state is stored as individually addressable rows in SQLite.
// Deploy one Go process against each DB file; this cache is not a distributed cache.
const schemaVersion = 2

func openStore(path string) (*store, error) {
	if path == "" {
		return nil, errors.New("DB_PATH is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Never open a legacy JSON file with the SQLite driver: preserve it intact.
	if probe, err := os.Open(path); err == nil {
		var header [16]byte
		n, readErr := probe.Read(header[:])
		_ = probe.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, readErr
		}
		if n > 0 && !bytes.Equal(header[:], []byte("SQLite format 3\x00")) {
			return nil, fmt.Errorf("DB_PATH %q is not SQLite (possibly legacy JSON); configure a separate .sqlite path and keep webpush.json for import", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Create a private database; the containing /data directory should also be private.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	var activeTx *sql.Tx
	closeOnErr := func(err error) (*store, error) {
		if activeTx != nil {
			_ = activeTx.Rollback()
			activeTx = nil
		}
		_ = conn.Close()
		return nil, err
	}
	// PRAGMAs are connection local (except journal_mode). FULL minimizes lost
	// acknowledged transactions after OS crashes. WAL supports consistent reads.
	for _, pragma := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
	} {
		if _, err := conn.Exec(pragma); err != nil {
			return closeOnErr(fmt.Errorf("%s: %w", pragma, err))
		}
	}
	tx, err := conn.Begin()
	activeTx = tx
	if err != nil {
		return closeOnErr(err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return closeOnErr(err)
	}
	if version > schemaVersion {
		return closeOnErr(fmt.Errorf("DB schema %d is newer than supported version %d", version, schemaVersion))
	}
	// Baseline migration (only schema version 1 exists so far).
	if version == 0 {
		ddl := []string{
			`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, payload BLOB NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS topics (name TEXT PRIMARY KEY, payload BLOB NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS devices (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, payload BLOB NOT NULL,
                FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_user ON devices(user_id, id)`,
			`CREATE TABLE IF NOT EXISTS invites (token_hash TEXT PRIMARY KEY, payload BLOB NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS messages (user_id TEXT NOT NULL, id TEXT NOT NULL,
                created_at TEXT NOT NULL, long_text TEXT NOT NULL DEFAULT '', payload BLOB NOT NULL,
                PRIMARY KEY(user_id, id), FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE)`,
			`CREATE INDEX IF NOT EXISTS idx_messages_user_created ON messages(user_id, created_at DESC)`,
			`CREATE TABLE IF NOT EXISTS deliveries (id TEXT PRIMARY KEY, message_id TEXT NOT NULL,
                user_id TEXT NOT NULL, device_id TEXT NOT NULL, status TEXT NOT NULL,
                next_attempt_at TEXT, payload BLOB NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_deliveries_due ON deliveries(status, next_attempt_at)`,
			`CREATE INDEX IF NOT EXISTS idx_deliveries_message ON deliveries(message_id, user_id)`,
			`CREATE TABLE IF NOT EXISTS idempotency (key_hash TEXT PRIMARY KEY, payload BLOB NOT NULL)`,
		}
		for _, statement := range ddl {
			if _, err := tx.Exec(statement); err != nil {
				return closeOnErr(fmt.Errorf("schema: %w", err))
			}
		}
		if _, err := tx.Exec(`PRAGMA user_version = 1`); err != nil {
			return closeOnErr(err)
		}
	}
	if version < 2 {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS message_log (id TEXT PRIMARY KEY, payload BLOB NOT NULL)`); err != nil {
			return closeOnErr(err)
		}
		if _, err := tx.Exec(`PRAGMA user_version = 2`); err != nil {
			return closeOnErr(err)
		}
	}
	s := &store{path: path, sqlDB: conn}
	var initialized string
	err = tx.QueryRow(`SELECT value FROM metadata WHERE key='initialized'`).Scan(&initialized)
	if errors.Is(err, sql.ErrNoRows) {
		var data database
		legacy := filepath.Join(filepath.Dir(path), "webpush.json")
		if filepath.Clean(legacy) != filepath.Clean(path) {
			raw, readErr := os.ReadFile(legacy)
			if readErr == nil {
				if err := json.Unmarshal(raw, &data); err != nil {
					return closeOnErr(fmt.Errorf("legacy JSON import %s: %w", legacy, err))
				}
				if data.Users == nil && data.Devices == nil && data.Topics == nil && data.Messages == nil {
					return closeOnErr(fmt.Errorf("legacy JSON %s lacks expected database fields", legacy))
				}
				initialized = "imported from " + filepath.Base(legacy)
			} else if !errors.Is(readErr, os.ErrNotExist) {
				return closeOnErr(readErr)
			}
		}
		data.initialize()
		if initialized == "" {
			for _, t := range []topic{{"system", "Systemmeldungen"}, {"backup", "Backup-Ergebnisse"}, {"automation", "Automationen"}} {
				item := t
				data.Topics[t.Name] = &item
			}
			initialized = "fresh"
		}
		var empty database
		empty.initialize()
		if err := applyChanges(tx, empty, data); err != nil {
			return closeOnErr(fmt.Errorf("initialize or import data: %w", err))
		}
		if _, err := tx.Exec(`INSERT INTO metadata(key,value) VALUES('initialized',?)`, initialized); err != nil {
			return closeOnErr(err)
		}
		s.db = data
	} else if err != nil {
		return closeOnErr(err)
	} else {
		s.db, err = loadDatabase(tx)
		if err != nil {
			return closeOnErr(err)
		}
	}
	if version < 2 {
		old := s.db
		old.MessageLog = map[string]*messageLogRecord{}
		backfillMessageLog(&s.db)
		if err := applyChanges(tx, old, s.db); err != nil {
			return closeOnErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return closeOnErr(err)
	}
	activeTx = nil
	// Retire old confirmation deadlines without resending accepted notifications.
	if err := s.update(func(db *database) error {
		for _, d := range db.Deliveries {
			if d.AcceptedAt != nil && (d.Status == "accepted" || d.Status == "unconfirmed") {
				d.Status = "accepted"
				d.NextAttemptAt = time.Time{}
				d.LastError = ""
			}
		}
		return nil
	}); err != nil {
		return closeOnErr(err)
	}
	return s, nil
}

// Close closes the SQLite handle; suitable for normal shutdown or tests.
func (s *store) Close() error { return s.sqlDB.Close() }

func loadDatabase(tx *sql.Tx) (database, error) {
	var d database
	d.initialize()
	for _, spec := range []struct {
		query string
		put   func(string, []byte) error
	}{
		{`SELECT id,payload FROM message_log`, func(k string, b []byte) error {
			var x messageLogRecord
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			d.MessageLog[k] = &x
			return nil
		}},
		{`SELECT id,payload FROM users`, func(k string, b []byte) error {
			var x user
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			d.Users[k] = &x
			return nil
		}},
		{`SELECT id,payload FROM devices`, func(k string, b []byte) error {
			var x device
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			d.Devices[k] = &x
			return nil
		}},
		{`SELECT name,payload FROM topics`, func(k string, b []byte) error {
			var x topic
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			d.Topics[k] = &x
			return nil
		}},
		{`SELECT token_hash,payload FROM invites`, func(k string, b []byte) error {
			var x invitation
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			d.Invites[k] = &x
			return nil
		}},
		{`SELECT id,payload FROM deliveries`, func(k string, b []byte) error {
			var x delivery
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			d.Deliveries[k] = &x
			return nil
		}},
		{`SELECT key_hash,payload FROM idempotency`, func(k string, b []byte) error {
			var x idempotencyRecord
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			d.Idempotency[k] = x
			return nil
		}},
	} {
		rows, err := tx.Query(spec.query)
		if err != nil {
			return d, err
		}
		for rows.Next() {
			var key string
			var payload []byte
			if err := rows.Scan(&key, &payload); err != nil {
				rows.Close()
				return d, err
			}
			if err := spec.put(key, payload); err != nil {
				rows.Close()
				return d, fmt.Errorf("decode %s: %w", spec.query, err)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return d, err
		}
	}
	rows, err := tx.Query(`SELECT user_id,id,long_text,payload FROM messages ORDER BY created_at,id`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var uid, id, longText string
		var payload []byte
		if err := rows.Scan(&uid, &id, &longText, &payload); err != nil {
			rows.Close()
			return d, err
		}
		var m inboxMessage
		if err := json.Unmarshal(payload, &m); err != nil {
			rows.Close()
			return d, err
		}
		m.LongText = longText
		d.Messages[uid] = append(d.Messages[uid], m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	var bootstrap string
	err = tx.QueryRow(`SELECT value FROM metadata WHERE key='bootstrap_used'`).Scan(&bootstrap)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	var keys string
	err = tx.QueryRow(`SELECT value FROM metadata WHERE key='integration_keys'`).Scan(&keys)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	if keys != "" {
		if err := json.Unmarshal([]byte(keys), &d.IntegrationKeys); err != nil {
			return d, err
		}
	}
	var disabled string
	err = tx.QueryRow(`SELECT value FROM metadata WHERE key='message_log_disabled'`).Scan(&disabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	d.MessageLogDisabled = disabled == "true"
	d.BootstrapUsed = bootstrap == "1"
	return d, nil
}

// update preserves the existing atomic mutation API but persists only changed
// SQLite rows. A callback failure or a SQL failure leaves both DB and cache unchanged.
func (s *store) update(fn func(*database) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(s.db)
	if err != nil {
		return err
	}
	var next database
	if err := json.Unmarshal(b, &next); err != nil {
		return err
	}
	next.initialize()
	if err := fn(&next); err != nil {
		return err
	}
	refreshMessageLog(&next)
	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(encoded) > 128*1024*1024 && len(encoded) > len(b) {
		return errors.New("storage capacity exceeded")
	}
	tx, err := s.sqlDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := applyChanges(tx, s.db, next); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyChanges(s.db, next)
	s.db = next
	return nil
}

// syncJSONRows writes changed per-entity rows and deletes removed entities.
// Extra indexed columns do not duplicate the full JSON payload.
func syncJSONRows[T any](tx *sql.Tx, old, next map[string]T, upsert, remove string, values func(string, T) ([]any, error)) error {
	for key := range old {
		if _, ok := next[key]; !ok {
			if _, err := tx.Exec(remove, key); err != nil {
				return err
			}
		}
	}
	keys := make([]string, 0, len(next))
	for k := range next {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		payload, err := json.Marshal(next[key])
		if err != nil {
			return err
		}
		if previous, ok := old[key]; ok {
			before, err := json.Marshal(previous)
			if err != nil {
				return err
			}
			if bytes.Equal(before, payload) {
				continue
			}
		}
		args, err := values(key, next[key])
		if err != nil {
			return err
		}
		args = append(args, payload)
		if _, err := tx.Exec(upsert, args...); err != nil {
			return err
		}
	}
	return nil
}

func applyChanges(tx *sql.Tx, old, next database) error {
	if old.MessageLogDisabled != next.MessageLogDisabled {
		if _, err := tx.Exec(`INSERT INTO metadata(key,value) VALUES('message_log_disabled',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.FormatBool(next.MessageLogDisabled)); err != nil {
			return err
		}
	}

	if err := syncJSONRows(tx, old.MessageLog, next.MessageLog,
		`INSERT INTO message_log(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`,
		`DELETE FROM message_log WHERE id=?`, func(k string, _ *messageLogRecord) ([]any, error) { return []any{k}, nil }); err != nil {
		return err
	}
	// Insert parent user records before devices and inbox foreign-key references.
	if err := syncJSONRows(tx, old.Users, next.Users,
		`INSERT INTO users(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`,
		`DELETE FROM users WHERE id=?`, func(k string, _ *user) ([]any, error) { return []any{k}, nil }); err != nil {
		return err
	}
	if err := syncJSONRows(tx, old.Deliveries, next.Deliveries,
		`INSERT INTO deliveries(id,message_id,user_id,device_id,status,next_attempt_at,payload) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET message_id=excluded.message_id,user_id=excluded.user_id,device_id=excluded.device_id,status=excluded.status,next_attempt_at=excluded.next_attempt_at,payload=excluded.payload`,
		`DELETE FROM deliveries WHERE id=?`,
		func(k string, d *delivery) ([]any, error) {
			return []any{k, d.MessageID, d.UserID, d.DeviceID, d.Status, sqliteTime(d.NextAttemptAt)}, nil
		}); err != nil {
		return err
	}
	if err := syncMessages(tx, old.Messages, next.Messages); err != nil {
		return err
	}
	if err := syncJSONRows(tx, old.Devices, next.Devices,
		`INSERT INTO devices(id,user_id,payload) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET user_id=excluded.user_id,payload=excluded.payload`,
		`DELETE FROM devices WHERE id=?`, func(k string, d *device) ([]any, error) { return []any{k, d.UserID}, nil }); err != nil {
		return err
	}
	if err := syncJSONRows(tx, old.Topics, next.Topics,
		`INSERT INTO topics(name,payload) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET payload=excluded.payload`,
		`DELETE FROM topics WHERE name=?`, func(k string, _ *topic) ([]any, error) { return []any{k}, nil }); err != nil {
		return err
	}
	if err := syncJSONRows(tx, old.Invites, next.Invites,
		`INSERT INTO invites(token_hash,payload) VALUES(?,?) ON CONFLICT(token_hash) DO UPDATE SET payload=excluded.payload`,
		`DELETE FROM invites WHERE token_hash=?`, func(k string, _ *invitation) ([]any, error) { return []any{k}, nil }); err != nil {
		return err
	}
	if err := syncJSONRows(tx, old.Idempotency, next.Idempotency,
		`INSERT INTO idempotency(key_hash,payload) VALUES(?,?) ON CONFLICT(key_hash) DO UPDATE SET payload=excluded.payload`,
		`DELETE FROM idempotency WHERE key_hash=?`, func(k string, _ idempotencyRecord) ([]any, error) { return []any{k}, nil }); err != nil {
		return err
	}
	beforeKeys, err := json.Marshal(old.IntegrationKeys)
	if err != nil {
		return err
	}
	nextKeys, err := json.Marshal(next.IntegrationKeys)
	if err != nil {
		return err
	}
	if !bytes.Equal(beforeKeys, nextKeys) {
		if _, err := tx.Exec(`INSERT INTO metadata(key,value) VALUES('integration_keys',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(nextKeys)); err != nil {
			return err
		}
	}
	if old.BootstrapUsed != next.BootstrapUsed {
		val := "0"
		if next.BootstrapUsed {
			val = "1"
		}
		if _, err := tx.Exec(`INSERT INTO metadata(key,value) VALUES('bootstrap_used',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, val); err != nil {
			return err
		}
	}
	return nil
}

func sqliteTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func syncMessages(tx *sql.Tx, old, next map[string][]inboxMessage) error {
	oldIndex := make(map[string]inboxMessage)
	nextIndex := make(map[string]inboxMessage)
	// length-prefixed key avoids ambiguities, but userId/messageId here are base64url.
	sep := "\x00"
	for uid, list := range old {
		for _, m := range list {
			oldIndex[uid+sep+m.ID] = m
		}
	}
	for uid, list := range next {
		for _, m := range list {
			nextIndex[uid+sep+m.ID] = m
		}
	}
	for key := range oldIndex {
		if _, ok := nextIndex[key]; !ok {
			i := strings.IndexByte(key, 0)
			if _, err := tx.Exec(`DELETE FROM messages WHERE user_id=? AND id=?`, key[:i], key[i+1:]); err != nil {
				return err
			}
		}
	}
	for key, m := range nextIndex {
		if prev, ok := oldIndex[key]; ok {
			left, _ := json.Marshal(prev)
			right, _ := json.Marshal(m)
			if bytes.Equal(left, right) {
				continue
			}
		}
		i := strings.IndexByte(key, 0)
		base := m
		base.LongText = "" // long text is stored once, in its own SQL column.
		payload, err := json.Marshal(base)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO messages(user_id,id,created_at,long_text,payload) VALUES(?,?,?,?,?) ON CONFLICT(user_id,id) DO UPDATE SET created_at=excluded.created_at,long_text=excluded.long_text,payload=excluded.payload`,
			key[:i], key[i+1:], m.CreatedAt.UTC().Format(time.RFC3339Nano), m.LongText, payload); err != nil {
			return err
		}
	}
	return nil
}
