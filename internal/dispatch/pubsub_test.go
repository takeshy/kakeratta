package dispatch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
	"google.golang.org/api/option"
)

func TestPublishAndAuthenticatedPush(t *testing.T) {
	job := store.Job{Workspace: "account", Reaction: "reaction", Dispatch: "generation"}
	var encoded string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/projects/test/topics/jobs:publish") {
			t.Errorf("publish path: %s", r.URL.Path)
		}
		var body struct {
			Messages []struct {
				Data string `json:"data"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Messages) != 1 {
			t.Error("expected one job")
			return
		}
		encoded = body.Messages[0].Data
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messageIds":["1"]}`))
	}))
	defer api.Close()
	p, err := New(context.Background(), "projects/test/topics/jobs", option.WithEndpoint(api.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Publish(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var published store.Job
	if err := json.Unmarshal(data, &published); err != nil || published != job {
		t.Fatalf("payload: %s %v", data, err)
	}
	validate := func(_ context.Context, token, audience string) (string, error) {
		if token != "token" || audience != "worker" {
			t.Error("wrong token audience")
		}
		return "runtime", nil
	}
	for _, test := range []struct {
		subscription, token string
		valid               bool
	}{{"subscription", "token", true}, {"other", "token", false}, {"subscription", "", false}} {
		body, _ := json.Marshal(map[string]any{"subscription": test.subscription, "message": map[string]string{"data": encoded}})
		r := httptest.NewRequest("POST", "/internal/work", strings.NewReader(string(body)))
		if test.token != "" {
			r.Header.Set("Authorization", "Bearer "+test.token)
		}
		got, err := ReadPush(r, validate, "worker", "runtime", "subscription")
		if (err == nil) != test.valid {
			t.Fatalf("auth %v: %v", test, err)
		}
		if test.valid && got != job {
			t.Fatalf("decoded job: %+v", got)
		}
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Authorization", "Bearer token")
	if err := Authenticate(r, validate, "worker", "different-account"); err == nil {
		t.Fatal("accepted another service account")
	}
}
