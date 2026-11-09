package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildVersionMatchesHTMLAssetsAndAPI(t *testing.T) {
	previous := buildVersion
	buildVersion = "20261108194830123456"
	t.Cleanup(func() { buildVersion = previous })
	h := testApp(t).routes()
	for _, path := range []string{"/", "/?message=one&_build=old", "/index.html", "/admin"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		body := w.Body.String()
		if w.Code != 200 || strings.Contains(body, "__PUSHIE_VERSION__") || !strings.Contains(body, `content="`+buildVersion+`"`) || !strings.Contains(body, "/app.js?v="+buildVersion) {
			t.Fatalf("%s: incorrect versioned page: %d", path, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-App-Version") != buildVersion {
			t.Fatal(w.Header())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/version?_=fresh", nil))
	var payload map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || payload["version"] != buildVersion {
		t.Fatal(payload, err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/sw.js", nil))
	if !strings.Contains(w.Body.String(), "const appBuildVersion='"+buildVersion+"'") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("service worker missing build identity")
	}
}

func TestExpiredSessionClearsCookieAndRequestsCacheClearing(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest("GET", "/api/me/messages", nil)
	r.Header.Set("Cookie", a.cookieName()+"=expired")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 401 || w.Header().Get("Clear-Site-Data") != `"cache"` {
		t.Fatal(w.Code, w.Header())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != a.cookieName() || cookies[0].MaxAge != -1 {
		t.Fatal("expired session cookie not removed", cookies)
	}
}
