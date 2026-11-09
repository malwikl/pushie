package main

import "testing"

func TestTopicManagementIncludesDefaults(t *testing.T) {
	a := testApp(t)
	addTestUser(t, a, "owner", "admin", []string{"system", "backup", "automation"})
	addTestUser(t, a, "member", "", []string{"system", "backup", "automation"})
	owner, member := signIn(t, a, "owner"), signIn(t, a, "member")
	h := a.routes()
	if r := request(t, h, "POST", "/api/admin/topics/web", map[string]string{"name": "custom"}, a.cfg.origin, "", member); r.Code != 403 {
		t.Fatalf("member created topic: %d", r.Code)
	}
	for _, name := range []string{"system", "backup", "automation"} {
		path := "/api/admin/topics/" + name + "/web"
		if r := request(t, h, "DELETE", path, nil, a.cfg.origin, "", member); r.Code != 403 {
			t.Fatalf("member deleted %s: %d", name, r.Code)
		}
		if r := request(t, h, "DELETE", path, nil, "https://evil.example", "", owner); r.Code != 403 {
			t.Fatalf("cross origin deletion: %d", r.Code)
		}
		if r := request(t, h, "DELETE", path, nil, a.cfg.origin, "", owner); r.Code != 200 {
			t.Fatalf("delete %s: %d %s", name, r.Code, r.Body.String())
		}
		for _, id := range []string{"owner", "member"} {
			if hasString(a.db.userByID(id).Topics, name) {
				t.Fatalf("subscription retained: %s %s", id, name)
			}
		}
	}
	reopened, err := openStore(a.db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.sqlDB.Close()
	if len(reopened.snapshot().Topics) != 0 {
		t.Fatal("default topics recreated on reload")
	}
	if r := request(t, h, "POST", "/api/admin/topics/web", map[string]string{"name": "system", "description": "New system"}, a.cfg.origin, "", owner); r.Code != 201 {
		t.Fatalf("recreate default: %d %s", r.Code, r.Body.String())
	}
	if r := request(t, h, "PUT", "/api/me/topics/system", nil, a.cfg.origin, "", member); r.Code != 200 {
		t.Fatalf("member subscribe: %d", r.Code)
	}
	if !hasString(a.db.userByID("member").Topics, "system") {
		t.Fatal("subscription missing")
	}
	if r := request(t, h, "DELETE", "/api/admin/topics/missing/web", nil, a.cfg.origin, "", owner); r.Code != 404 {
		t.Fatalf("missing topic: %d", r.Code)
	}
}
