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
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
