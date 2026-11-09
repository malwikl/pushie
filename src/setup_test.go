package main

import (
	"bytes"
	webpush "github.com/SherClockHolmes/webpush-go"
	"strings"
	"testing"
)

func TestSetupGeneratesCopyableValues(t *testing.T) {
	for _, name := range []string{"VAPID_PUBLIC_KEY", "VAPID_PRIVATE_KEY", "SEND_TOKEN", "ADMIN_TOKEN", "BOOTSTRAP_OTP"} {
		t.Setenv(name, "")
	}
	var out bytes.Buffer
	generated, err := printSetupValues(&out)
	if err != nil || !generated {
		t.Fatalf("setup: %v", err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatal(line)
		}
		values[k] = v
		t.Setenv(k, v)
	}
	if len(values) != 5 || !validVAPIDPair(values["VAPID_PUBLIC_KEY"], values["VAPID_PRIVATE_KEY"]) {
		t.Fatal("invalid generated credentials")
	}
	if values["SEND_TOKEN"] == values["ADMIN_TOKEN"] || values["BOOTSTRAP_OTP"] == values["SEND_TOKEN"] {
		t.Fatal("duplicate secrets")
	}
	out.Reset()
	generated, err = printSetupValues(&out)
	if err != nil || generated || out.Len() != 0 {
		t.Fatal("valid configuration regenerated or logged")
	}
	t.Setenv("BOOTSTRAP_OTP", "")
	generated, err = printSetupValues(&out)
	if err != nil || generated {
		t.Fatal("disabled bootstrap regenerated")
	}
	t.Setenv("ADMIN_TOKEN", values["SEND_TOKEN"])
	generated, err = printSetupValues(&out)
	if err != nil || !generated || strings.Contains(out.String(), values["SEND_TOKEN"]) {
		t.Fatal("duplicate token not replaced or existing secret logged")
	}
}
func TestVAPIDValidation(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	if !validVAPIDPair(pub, priv) || validVAPIDPair(pub, other) || validVAPIDPair("replace", priv) || validVAPIDPair(pub, "") {
		t.Fatal("incorrect pair validation")
	}
}
