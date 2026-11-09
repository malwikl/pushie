package main

import (
	"bytes"
	"testing"
)

func TestWebUserManagement(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "owner", "admin", nil)
	addTestUser(t, a, "member", "", nil)
	owner := signIn(t, a, "owner")
	member := signIn(t, a, "member")
	h := a.routes()
	for _, op := range []struct{ method, path string }{
		{"GET", "/api/admin/users/web"},
		{"POST", "/api/admin/users/owner/promote/web"},
		{"DELETE", "/api/admin/users/owner/web"},
	} {
		if r := request(t, h, op.method, op.path, nil, a.cfg.origin, "", member); r.Code != 403 {
			t.Fatalf("member %s: %d", op.path, r.Code)
		}
	}
	if r := request(t, h, "POST", "/api/admin/users/member/promote/web", nil, "https://evil.example", "", owner); r.Code != 403 {
		t.Fatalf("cross origin: %d", r.Code)
	}
	if r := request(t, h, "GET", "/api/admin/users/web", nil, "", "", owner); r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"role":"admin"`)) {
		t.Fatalf("list: %d %s", r.Code, r.Body.String())
	}
	if r := request(t, h, "POST", "/api/admin/users/member/promote/web", nil, a.cfg.origin, "", owner); r.Code != 200 || !a.db.userByID("member").isAdmin() {
		t.Fatalf("promote: %d", r.Code)
	}
	if r := request(t, h, "DELETE", "/api/admin/users/owner/web", nil, a.cfg.origin, "", owner); r.Code != 409 {
		t.Fatalf("self deletion: %d", r.Code)
	}
	if err := a.db.update(func(d *database) error {
		d.Devices["member-device"] = &device{ID: "member-device", UserID: "member"}
		d.Devices["owner-device"] = &device{ID: "owner-device", UserID: "owner"}
		d.Messages["member"] = []inboxMessage{{ID: "member-message"}}
		d.Messages["owner"] = []inboxMessage{{ID: "owner-message"}}
		d.Deliveries["member-job"] = &delivery{ID: "member-job", UserID: "member", DeviceID: "member-device"}
		d.Deliveries["owner-job"] = &delivery{ID: "owner-job", UserID: "owner", DeviceID: "owner-device"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if r := request(t, h, "DELETE", "/api/admin/users/member/web", nil, a.cfg.origin, "", owner); r.Code != 200 {
		t.Fatalf("delete: %d %s", r.Code, r.Body.String())
	}
	snap := a.db.snapshot()
	if snap.Users["member"] != nil || snap.Devices["member-device"] != nil || len(snap.Messages["member"]) != 0 || snap.Deliveries["member-job"] != nil {
		t.Fatal("deleted account data retained")
	}
	if snap.Users["owner"] == nil || snap.Devices["owner-device"] == nil || len(snap.Messages["owner"]) != 1 || snap.Deliveries["owner-job"] == nil {
		t.Fatal("other account modified")
	}
	if r := request(t, h, "GET", "/api/me", nil, "", "", member); r.Code != 401 {
		t.Fatalf("deleted session: %d", r.Code)
	}
	if r := request(t, h, "DELETE", "/api/admin/users/member/web", nil, a.cfg.origin, "", owner); r.Code != 404 {
		t.Fatalf("missing user: %d", r.Code)
	}
}
