package main

import (
	"net/http"
	"strings"
)

// Set by Docker and scripts/build.sh at compilation, never at server startup.
var buildVersion = "0"

func serveVersionedAsset(name, contentType string) http.HandlerFunc {
	data, err := assets.ReadFile("static/" + name)
	if err != nil {
		panic(err)
	}
	body := strings.ReplaceAll(string(data), "__PUSHIE_VERSION__", buildVersion)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(body))
	}
}
