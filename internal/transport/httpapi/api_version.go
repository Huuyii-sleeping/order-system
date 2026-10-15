package httpapi

import (
	"fmt"
	"net/http"
	"strings"
)

const (
	APIV1Prefix        = "/api/v1"
	APIVersionV1       = "v1"
	APIVersionHeader   = "API-Version"
	apiLegacyAccess    = "legacy"
	apiVersionedAccess = "versioned"

	// RFC 9745 uses an sf-date value. This timestamp is the date on which the
	// unversioned compatibility entrypoint became deprecated; it is not a
	// removal deadline. A Sunset header must only be added after an owner has
	// approved a concrete removal date.
	legacyDeprecationDate = "@1792022400"
)

type APILifecycleMetrics interface {
	RecordAPIAccess(version, entrypoint string)
}

// apiRouteSet mounts the same protocol handler at a canonical versioned path
// and at its temporary unversioned compatibility path. Keeping this concern in
// the transport layer avoids duplicating application or domain behavior.
type apiRouteSet struct {
	mux        *http.ServeMux
	prefix     string
	entrypoint string
	metrics    APILifecycleMetrics
}

func (r apiRouteSet) Handle(pattern string, handler http.Handler) {
	r.mux.Handle(prefixedPattern(pattern, r.prefix), r.withLifecycle(handler))
}

func (r apiRouteSet) HandleFunc(pattern string, handler http.HandlerFunc) {
	r.Handle(pattern, handler)
}

func (r apiRouteSet) withLifecycle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set(APIVersionHeader, APIVersionV1)
		if r.entrypoint == apiLegacyAccess {
			w.Header().Set("Deprecation", legacyDeprecationDate)
			successor := APIV1Prefix + request.URL.EscapedPath()
			w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"successor-version\"", successor))
		}
		if r.metrics != nil {
			r.metrics.RecordAPIAccess(APIVersionV1, r.entrypoint)
		}
		next.ServeHTTP(w, request)
	})
}

func prefixedPattern(pattern, prefix string) string {
	method, path, found := strings.Cut(strings.TrimSpace(pattern), " ")
	if !found || method == "" || !strings.HasPrefix(path, "/") {
		panic("httpapi: invalid method/path pattern " + pattern)
	}
	return method + " " + prefix + path
}
