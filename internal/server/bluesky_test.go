package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/takeshy/kakeratta/internal/social"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type oauthTransport func(*http.Request) (*http.Response, error)

func (f oauthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func oauthResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}
func setupOAuthProvider(t *testing.T, s *Server) (*string, *int) {
	t.Helper()
	var state, challenge string
	refreshed := false
	posts := 0
	dir := identity.NewMockDirectory()
	dir.Insert(identity.Identity{DID: syntax.DID("did:plc:alice"), Handle: syntax.Handle("alice.bsky.social"), Services: map[string]identity.ServiceEndpoint{"atproto_pds": {URL: "https://pds.example.com", Type: "AtprotoPersonalDataServer"}}})
	client := &http.Client{Transport: oauthTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			return oauthResponse(200, `{"authorization_servers":["https://auth.example.com"]}`), nil
		case "/.well-known/oauth-authorization-server":
			return oauthResponse(200, `{
   "issuer":"https://auth.example.com","authorization_endpoint":"https://auth.example.com/authorize","token_endpoint":"https://auth.example.com/token","revocation_endpoint":"https://auth.example.com/revoke","pushed_authorization_request_endpoint":"https://auth.example.com/par",
   "response_types_supported":["code"],"grant_types_supported":["authorization_code","refresh_token"],"code_challenge_methods_supported":["S256"],"token_endpoint_auth_methods_supported":["none","private_key_jwt"],"token_endpoint_auth_signing_alg_values_supported":["ES256"],"scopes_supported":["atproto"],"authorization_response_iss_parameter_supported":true,"require_pushed_authorization_requests":true,"dpop_signing_alg_values_supported":["ES256"],"client_id_metadata_document_supported":true
  }`), nil
		case "/par":
			r.ParseForm()
			state = r.Form.Get("state")
			challenge = r.Form.Get("code_challenge")
			if r.Header.Get("DPoP") == "" || r.Form.Get("client_assertion") == "" || r.Form.Get("scope") != strings.Join(social.Scopes, " ") {
				t.Error("missing OAuth proof or wrong scopes")
			}
			return oauthResponse(201, `{"request_uri":"urn:request:one","expires_in":600}`), nil
		case "/token":
			r.ParseForm()
			if r.Form.Get("grant_type") == "authorization_code" && oauth.S256CodeChallenge(r.Form.Get("code_verifier")) != challenge {
				t.Error("PKCE mismatch")
			}
			access, refresh := "access", "refresh"
			if r.Form.Get("grant_type") == "refresh_token" {
				if r.Form.Get("refresh_token") != "refresh" {
					t.Error("wrong refresh token")
				}
				refreshed = true
				access, refresh = "access2", "refresh2"
			}
			return oauthResponse(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":%q,"token_type":"DPoP","sub":"did:plc:alice","scope":%q}`, access, refresh, strings.Join(social.Scopes, " "))), nil
		case "/revoke":
			return oauthResponse(200, `{}`), nil
		case "/xrpc/com.atproto.repo.createRecord":
			if !refreshed {
				response := oauthResponse(401, `{"error":"ExpiredToken"}`)
				response.Header.Set("WWW-Authenticate", `DPoP error="invalid_token"`)
				return response, nil
			}
			posts++
			if r.Header.Get("Authorization") != "DPoP access2" || r.Header.Get("DPoP") == "" {
				t.Error("publication missing DPoP")
			}
			var body struct {
				Repo string `json:"repo"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Repo != "did:plc:alice" {
				t.Error("wrong user published")
			}
			return oauthResponse(200, `{"uri":"at://did:plc:alice/app.bsky.feed.post/abc","cid":"cid"}`), nil
		}
		t.Errorf("unexpected OAuth endpoint %s", r.URL.Path)
		return oauthResponse(404, `{}`), nil
	})}
	s.oauthClient = func(app *oauth.ClientApp) { app.Dir = dir; app.Client = client; app.Resolver.Client = client }
	return &state, &posts
}
func TestBlueskyOAuthConnectPublishAndDisconnect(t *testing.T) {
	s, w := socialApp(t)
	_, persistence, _ := s.blueskyOAuth()
	persistence.SaveConnection(context.Background(), nil)
	state, posts := setupOAuthProvider(t, s)
	start := socialRequest(s, "/api/social/bluesky/connect", map[string]string{"handle": "alice.bsky.social", "returnRoot": "p"})
	if start.Code != 200 {
		t.Fatal(start.Body.String())
	}
	var auth map[string]string
	json.Unmarshal(start.Body.Bytes(), &auth)
	if !strings.HasPrefix(auth["url"], "https://auth.example.com/authorize?") || len(auth["browserSecret"]) != 64 {
		t.Fatal(auth)
	}
	params := url.Values{"state": {*state}, "iss": {"https://auth.example.com"}, "code": {"code"}}
	wrong := socialRequest(s, "/api/social/bluesky/finish", map[string]string{"params": params.Encode(), "browserSecret": strings.Repeat("x", 64)})
	if wrong.Code != 400 {
		t.Fatal("accepted wrong browser")
	}
	finished := socialRequest(s, "/api/social/bluesky/finish", map[string]string{"params": params.Encode(), "browserSecret": auth["browserSecret"]})
	if finished.Code != 200 || !strings.Contains(finished.Body.String(), "alice.bsky.social") {
		t.Fatal(finished.Body.String())
	}
	replay := socialRequest(s, "/api/social/bluesky/finish", map[string]string{"params": params.Encode(), "browserSecret": auth["browserSecret"]})
	if replay.Code != 400 {
		t.Fatal("callback replay")
	}
	w.Append(timeline.Entry{ID: "p", Root: "p", Speaker: "user", Kind: timeline.KindPost, Text: "hello"})
	body := socialBody{Root: "p", Target: "bluesky", EntryIDs: []string{"p"}}
	preview := socialRequest(s, "/api/social/preview", body)
	var plan social.Plan
	json.Unmarshal(preview.Body.Bytes(), &plan)
	body.Digest = plan.Digest
	published := socialRequest(s, "/api/social/publish", body)
	if published.Code != 200 || *posts != 1 {
		t.Fatalf("%d posts: %s", *posts, published.Body.String())
	}
	stored, err := persistence.GetSession(context.Background(), syntax.DID("did:plc:alice"), *state)
	if err != nil || stored.RefreshToken != "refresh2" {
		t.Fatal("rotated tokens not persisted", err)
	}
	recorder := httptest.NewRecorder()
	s.ServeHTTP(recorder, httptest.NewRequest("DELETE", "/api/social/bluesky/connection", nil))
	if recorder.Code != 200 {
		t.Fatal(recorder.Body.String())
	}
	if _, err := persistence.Connection(context.Background()); err == nil {
		t.Fatal("still connected")
	}
	if _, err := persistence.GetSession(context.Background(), syntax.DID("did:plc:alice"), *state); err == nil {
		t.Fatal("retained access tokens")
	}
	after := socialRequest(s, "/api/social/preview", body)
	if after.Code == 200 {
		t.Fatal("can publish after disconnect")
	}
}
func TestBlueskyMetadataPublicAndNoSecrets(t *testing.T) {
	s, _ := socialApp(t)
	recorder := httptest.NewRecorder()
	s.ServeHTTP(recorder, httptest.NewRequest("GET", social.MetadataPath, nil))
	if recorder.Code != 200 {
		t.Fatal(recorder.Body.String())
	}
	var metadata oauth.ClientMetadata
	json.Unmarshal(recorder.Body.Bytes(), &metadata)
	if err := metadata.Validate("https://example.com" + social.MetadataPath); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(recorder.Body.String(), strings.Repeat("01", 32)) || metadata.JWKS == nil || metadata.Scope != strings.Join(social.Scopes, " ") {
		t.Fatal("unsafe metadata")
	}
}
func TestBlueskyFlowCannotFinishInAnotherWorkspace(t *testing.T) {
	a, _ := socialApp(t)
	state, _ := setupOAuthProvider(t, a)
	start := socialRequest(a, "/api/social/bluesky/connect", map[string]string{"handle": "alice.bsky.social"})
	var auth map[string]string
	json.Unmarshal(start.Body.Bytes(), &auth)
	b, _ := app(t)
	setupOAuthProvider(t, b)
	params := url.Values{"state": {*state}, "iss": {"https://auth.example.com"}, "code": {"code"}}
	result := socialRequest(b, "/api/social/bluesky/finish", map[string]string{"params": params.Encode(), "browserSecret": auth["browserSecret"]})
	if result.Code != 400 {
		t.Fatal("cross-user connection")
	}
}

func TestBlueskyMetadataBypassesGatewayLoginOnlyForMetadata(t *testing.T) {
	socialApp(t)
	gateway := &Gateway{}
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, httptest.NewRequest("GET", social.MetadataPath, nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, httptest.NewRequest("GET", "/api/social/bluesky/connection", nil))
	if w.Code != 401 {
		t.Fatal("private connection endpoint exposed", w.Code)
	}
}
