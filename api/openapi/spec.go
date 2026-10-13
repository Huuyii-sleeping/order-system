// Package openapi exposes the machine-readable HTTP API contract.
package openapi

import (
	_ "embed"
	"net/http"
)

//go:embed spec.json
var document []byte

// Document returns a copy so callers cannot mutate the embedded contract.
func Document() []byte {
	return append([]byte(nil), document...)
}

// Handler serves the exact document checked by contract tests and CI.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oai.openapi+json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(document)
	})
}
