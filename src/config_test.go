package main

import (
	webpush "github.com/SherClockHolmes/webpush-go"
	"testing"
)

func TestConfigDerivesPublicSettings(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"VAPID_PUBLIC_KEY": pub, "VAPID_PRIVATE_KEY": priv, "SEND_TOKEN": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "ADMIN_TOKEN": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "BOOTSTRAP_OTP": "", "VAPID_SUBJECT": "", "ORIGIN": "https://push.example.com:8443", "RP_ID": "obsolete.example.com", "TRUSTED_PROXIES": ""} {
		t.Setenv(name, value)
	}
	c, err := readConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.rpID != "push.example.com" || c.subject != c.origin || !c.secureCookies {
		t.Fatalf("incorrect derived config: hostname=%s subject=%s", c.rpID, c.subject)
	}
	t.Setenv("VAPID_SUBJECT", "mailto:admin@example.com")
	c, err = readConfig()
	if err != nil || c.subject != "mailto:admin@example.com" {
		t.Fatal("contact override failed", err)
	}
	for _, origin := range []string{"https://user:pass@example.com", "https://example.com/path", "https://example.com?query", "https://example.com#fragment", "http://example.com", "https://"} {
		t.Setenv("ORIGIN", origin)
		if _, err := readConfig(); err == nil {
			t.Fatalf("accepted invalid origin %s", origin)
		}
	}
	t.Setenv("ORIGIN", "http://localhost:8099")
	c, err = readConfig()
	if err != nil || c.rpID != "localhost" || c.secureCookies {
		t.Fatal("local config failed", err)
	}
}
