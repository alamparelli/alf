package controlcenter

import "net/http"

// WithExperimentalHeader wraps h so every response carries
// X-ALF-Experimental: no-isolation. Paired with the boot-time gate in
// cmd/alf-daemon/experimental.go so UI layers and curl users can see the
// dev-window state without re-reading the env var.
//
// Removed in the ticket that flips ALF_OCAP_STRICT=1 (see #406 and
// docs/ARCHITECTURE-SECURITY.md §12).
func WithExperimentalHeader(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ALF-Experimental", "no-isolation")
		h.ServeHTTP(w, r)
	})
}
