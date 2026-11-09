package main

import (
	"crypto/ecdh"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func validVAPIDPair(public, private string) bool {
	decode := func(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")) }
	priv, err := decode(private)
	if err != nil || len(priv) != 32 {
		return false
	}
	key, err := ecdh.P256().NewPrivateKey(priv)
	if err != nil {
		return false
	}
	pub, err := decode(public)
	return err == nil && constantBytesEqual(pub, key.PublicKey().Bytes())
}

func validSetupToken(value string, min, max int) bool {
	return len(value) >= min && len(value) <= max && !strings.Contains(strings.ToLower(value), "replace") && !strings.ContainsAny(value, " \t\r\n")
}

// Setup prints only newly generated values, never existing configured secrets.
// Do not serve with ephemeral credentials: the operator must save them first.
func printSetupValues(w io.Writer) (bool, error) {
	values := [][2]string{}
	if !validVAPIDPair(env("VAPID_PUBLIC_KEY"), env("VAPID_PRIVATE_KEY")) {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return false, err
		}
		values = append(values, [2]string{"VAPID_PUBLIC_KEY", pub}, [2]string{"VAPID_PRIVATE_KEY", priv})
	}
	send, admin, bootstrap := env("SEND_TOKEN"), env("ADMIN_TOKEN"), env("BOOTSTRAP_OTP")
	for _, entry := range []struct {
		name     string
		value    *string
		min, max int
	}{
		{"SEND_TOKEN", &send, 32, 4096}, {"ADMIN_TOKEN", &admin, 32, 4096}, {"BOOTSTRAP_OTP", &bootstrap, 20, 256},
	} {
		duplicate := entry.name == "ADMIN_TOKEN" && admin == send || entry.name == "BOOTSTRAP_OTP" && (bootstrap == send || bootstrap == admin)
		// An explicitly empty bootstrap code can disable setup after initial registration.
		if entry.name == "BOOTSTRAP_OTP" && bootstrap == "" && len(values) == 0 {
			continue
		}
		if !validSetupToken(*entry.value, entry.min, entry.max) || duplicate {
			value, err := secret()
			if err != nil {
				return false, err
			}
			*entry.value = value
			values = append(values, [2]string{entry.name, value})
		}
	}
	if len(values) == 0 {
		return false, nil
	}
	if _, err := fmt.Fprintln(w, "# Configuration setup: paste these generated values into .env, then recreate the container."); err != nil {
		return false, err
	}
	for _, entry := range values {
		if _, err := fmt.Fprintf(w, "%s=%s\n", entry[0], entry[1]); err != nil {
			return false, err
		}
	}
	_, err := fmt.Fprintln(w, "# Server not started. Set ORIGIN to your public HTTPS address, then run: docker compose up -d --force-recreate")
	return true, err
}
