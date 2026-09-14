package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
)

type multipartFile = multipart.File
type multipartHeader = multipart.FileHeader

// cookieName is how the page keeps the token after presenting it once, so the
// share target - which posts without a chance to set a header - is already
// authorised when it arrives. It is __session because that is the one cookie
// Firebase Hosting lets through to Cloud Run (see authCookieName).
const cookieName = "__session"

// guard checks the token.
//
// With no token set the app is open, which is what you want on a laptop and
// exactly what you do not want on a server holding inference keys. The shortcut
// on iOS sends it as a header, the share target inherits the cookie, and a
// browser gets the cookie the first time it presents one.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := s.workspaceKey
		if key == "" {
			key = fmt.Sprintf("%x", sha256.Sum256([]byte(s.store.Root()+"\x00"+s.token)))
		}
		wanted := r.Header.Get("X-Kakeratta-Workspace")
		if r.URL.Path == "/api/events" {
			wanted = r.URL.Query().Get("workspace")
		}
		if wanted != "" && wanted != key {
			fail(w, http.StatusUnauthorized, "account changed")
			return
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			// ParseMultipartForm's argument only limits memory, not body size.
			// Apply the limit before authentication can parse a form token.
			r.Body = http.MaxBytesReader(w, r.Body, maxCapture+(1<<20))
			defer func() {
				if r.MultipartForm != nil {
					_ = r.MultipartForm.RemoveAll()
				}
			}()
		}
		if s.token == "" {
			next(w, r)
			return
		}
		if cookie, err := r.Cookie(cookieName); err == nil && same(cookie.Value, s.token) {
			next(w, r)
			return
		}
		presented := bearer(r.Header.Get("Authorization"))
		if presented == "" {
			presented = r.URL.Query().Get("token")
		}
		if presented == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			// The share target and the shortcut can put it in the form.
			if err := r.ParseMultipartForm(maxCapture); err != nil {
				fail(w, http.StatusBadRequest, "受け取れませんでした")
				return
			}
			presented = r.FormValue("token")
		}
		if !same(presented, s.token) {
			fail(w, http.StatusUnauthorized, "トークンが必要です")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: cookieName, Value: s.token, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 60 * 60 * 24 * 365,
			Secure: requestIsHTTPS(r),
		})
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
