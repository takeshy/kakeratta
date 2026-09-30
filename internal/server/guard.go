package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// guard validates the workspace.
// Firebase authentication is handled by Gateway.
//
// A change must come from the page itself. A local app has no login, and a
// form or a text/plain fetch from any other site reaches it without a
// preflight, so a request whose Origin is another site's is refused before it
// can post, install a persona or change a setting.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !sameOrigin(r) {
				fail(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		}
		key := s.workspaceKey
		if key == "" {
			key = fmt.Sprintf("%x", sha256.Sum256([]byte(s.store.Root()+"\x00")))
		}
		wanted := r.Header.Get("X-Kakeratta-Workspace")
		if r.URL.Path == "/api/events" {
			wanted = r.URL.Query().Get("workspace")
		}
		if wanted != "" && wanted != key {
			fail(w, http.StatusUnauthorized, "account changed")
			return
		}
		next(w, r)
	}
}

func bearer(header string) string {
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func same(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
