package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNotificationSchemaMigrationPreservesV2Data(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "alice", "", nil)
	if err := a.db.update(func(db *database) error {
		db.NotificationLogDisabled = true
		db.Notifications["alice"] = []inboxNotification{{ID: "preserved", Body: "Original content", CreatedAt: time.Now().UTC()}}
		db.Deliveries["job"] = &delivery{ID: "job", NotificationID: "preserved", UserID: "alice", DeviceID: "phone", Status: "retry"}
		db.Idempotency["retry-key"] = idempotencyRecord{Result: sendResult{NotificationID: "preserved"}, CreatedAt: time.Now().UTC()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE notifications RENAME TO messages`,
		`DROP INDEX idx_notifications_user_created`,
		`CREATE INDEX idx_messages_user_created ON messages(user_id,created_at DESC)`,
		`ALTER TABLE notification_log RENAME TO message_log`,
		`ALTER TABLE deliveries RENAME COLUMN notification_id TO message_id`,
		`DROP INDEX idx_deliveries_notification`,
		`CREATE INDEX idx_deliveries_message ON deliveries(message_id,user_id)`,
		`UPDATE metadata SET key='message_log_disabled' WHERE key='notification_log_disabled'`,
		`UPDATE deliveries SET payload=json_remove(json_set(CAST(payload AS TEXT),'$.messageId',json_extract(CAST(payload AS TEXT),'$.notificationId')),'$.notificationId')`,
		`UPDATE idempotency SET payload=json_remove(json_set(CAST(payload AS TEXT),'$.result.messageId',json_extract(CAST(payload AS TEXT),'$.result.notificationId')),'$.result.notificationId')`,
		`PRAGMA user_version=2`,
	} {
		if _, err := a.db.sqlDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	migrated, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	data := migrated.snapshot()
	if len(data.Notifications["alice"]) != 1 || data.Notifications["alice"][0].Body != "Original content" || data.Deliveries["job"].NotificationID != "preserved" || data.Idempotency["retry-key"].Result.NotificationID != "preserved" || !data.NotificationLogDisabled {
		t.Fatal("migration lost stored data")
	}
	var version, obsolete int
	if err = migrated.sqlDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 3 {
		t.Fatal("wrong schema version")
	}
	if err = migrated.sqlDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('messages','message_log','idx_deliveries_message','idx_messages_user_created')`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatal("obsolete schema remains")
	}
	for _, table := range []string{"deliveries", "idempotency"} {
		var raw []byte
		if err = migrated.sqlDB.QueryRow(`SELECT payload FROM ` + table + ` LIMIT 1`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err = json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if table == "idempotency" {
			payload = payload["result"].(map[string]any)
		}
		if _, ok := payload["messageId"]; ok {
			t.Fatal("obsolete JSON field remains")
		}
	}
}
