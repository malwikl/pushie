package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Version 3 renames stored data transactionally. Old names occur only here,
// never as runtime aliases or duplicate data.
func migrateNotificationSchema(tx *sql.Tx, version int) error {
	statements := []string{
		`ALTER TABLE messages RENAME TO notifications`,
		`DROP INDEX IF EXISTS idx_messages_user_created`,
		`CREATE INDEX idx_notifications_user_created ON notifications(user_id, created_at DESC)`,
		`ALTER TABLE deliveries RENAME COLUMN message_id TO notification_id`,
		`DROP INDEX IF EXISTS idx_deliveries_message`,
		`CREATE INDEX idx_deliveries_notification ON deliveries(notification_id, user_id)`,
		`UPDATE metadata SET key='notification_log_disabled' WHERE key='message_log_disabled'`,
	}
	if version == 2 {
		statements = append(statements, `ALTER TABLE message_log RENAME TO notification_log`)
	} else {
		statements = append(statements, `CREATE TABLE notification_log (id TEXT PRIMARY KEY,payload BLOB NOT NULL)`)
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("notification schema migration: %w", err)
		}
	}
	for _, table := range []string{"deliveries", "idempotency"} {
		rows, err := tx.Query(`SELECT rowid,payload FROM ` + table)
		if err != nil {
			return err
		}
		type entry struct {
			id   int64
			data []byte
		}
		var entries []entry
		for rows.Next() {
			var id int64
			var raw []byte
			if err = rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			var data map[string]any
			if err = json.Unmarshal(raw, &data); err != nil {
				rows.Close()
				return err
			}
			if table == "deliveries" {
				renameStoredNotificationID(data)
			} else if result, ok := data["result"].(map[string]any); ok {
				renameStoredNotificationID(result)
			}
			encoded, err := json.Marshal(data)
			if err != nil {
				rows.Close()
				return err
			}
			entries = append(entries, entry{id, encoded})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range entries {
			if _, err = tx.Exec(`UPDATE `+table+` SET payload=? WHERE rowid=?`, e.data, e.id); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(`PRAGMA user_version = 3`)
	return err
}
func renameStoredNotificationID(data map[string]any) {
	if id, ok := data["messageId"]; ok {
		data["notificationId"] = id
		delete(data, "messageId")
	}
}
