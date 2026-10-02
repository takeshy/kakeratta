package persona

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
)

func TestAnswerRouterConfiguration(t *testing.T) {
	for _, mode := range []string{"", "lightning", "switchyard", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("ROUTER_MODE", mode)
			t.Setenv("SWITCHYARD_BASE_URL", "http://localhost:4000/v1/")
			t.Setenv("SWITCHYARD_API_KEY", "proxy-key")
			t.Setenv("SWITCHYARD_MODEL", "auto")
			t.Setenv("SWITCHYARD_ULTRA_MODEL", "fixed")
			r, err := AnswerRouterFromEnv()
			if mode == "invalid" {
				if err == nil {
					t.Fatal("invalid mode accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			original := llm.New("https://provider.example/v1", "workspace-key")
			c, m, slot := r.Select(original, "middle", "super", false)
			if mode != "switchyard" {
				if c != original || m != "middle" || slot != "super" {
					t.Fatal("default routing changed")
				}
				return
			}
			if c.APIKey != "proxy-key" || c.OpenRouter || m != "auto" || slot != "switchyard" {
				t.Fatalf("wrong proxy selection: %v %s %s", c, m, slot)
			}
			_, m, slot = r.Select(original, "high", "ultra", true)
			if m != "fixed" || slot != "ultra" {
				t.Fatal("explicit Ultra lost")
			}
		})
	}
	t.Setenv("ROUTER_MODE", "switchyard")
	for _, base := range []string{"localhost:4000", "file:///tmp/router", "https://user:pass@host/v1", "https://host/v1?key=secret"} {
		t.Setenv("SWITCHYARD_BASE_URL", base)
		if _, err := AnswerRouterFromEnv(); err == nil {
			t.Fatalf("accepted %s", base)
		}
	}
}

func TestSwitchyardResearchAndAnswerBoundary(t *testing.T) {
	for _, tc := range []struct{ deep, fail bool }{{false, false}, {true, false}, {false, true}} {
		t.Run(strings.Join([]string{map[bool]string{true: "ultra", false: "auto"}[tc.deep], map[bool]string{true: "failure", false: "success"}[tc.fail]}, "/"), func(t *testing.T) {
			r, s, direct := setup(t, func(system, user string) string {
				if strings.Contains(system, "明示的に求めているか") {
					t.Error("Lightning escalation called in Switchyard mode")
				}
				return `{"deliver":["gourmet"]}`
			}, "gourmet")
			skill, _ := r.catalogue.Find(context.Background(), "gourmet")
			skill.AlwaysUltra = tc.deep
			raw, _ := json.Marshal(skill)
			if _, err := r.catalogue.Update(context.Background(), skill.ID, raw, nil); err != nil {
				t.Fatal(err)
			}
			calls := 0
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.URL.Path != "/v1/chat/completions" || req.Header.Get("Authorization") != "Bearer proxy-key" {
					t.Error("wrong proxy path or credential")
				}
				var body struct {
					Model string
					Tools []struct{ Function struct{ Name string } }
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				want := "auto"
				if tc.deep {
					want = "fixed"
				}
				if body.Model != want {
					t.Errorf("model=%s want=%s", body.Model, want)
				}
				if tc.fail {
					http.Error(w, "unavailable", 503)
					return
				}
				name, args := "finish_read", `{}`
				if body.Tools[0].Function.Name == "submit_answer" {
					name, args = "submit_answer", `{"text":"proxy answer","sources":[]}`
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"id": "c", "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}}}})
			}))
			defer proxy.Close()
			r.SetAnswerRouter(AnswerRouter{Mode: "switchyard", Client: llm.New(proxy.URL+"/v1", "proxy-key"), Model: "auto", UltraModel: "fixed"})
			entry := post(t, s, "@tabe 質問です")
			r.react(entry.ID)
			said := remarks(t, s)
			if calls == 0 || len(said) != 1 {
				t.Fatalf("calls=%d remarks=%v", calls, said)
			}
			if tc.fail {
				if said[0].Model != "" || said[0].Text == "proxy answer" {
					t.Fatal("failure presented as success")
				}
			} else {
				slot := "switchyard"
				if tc.deep {
					slot = "ultra"
				}
				if said[0].Text != "proxy answer" || said[0].Model != slot {
					t.Fatalf("wrong answer: %+v", said[0])
				}
				r.react(replyTo(t, s, said[0], "もっと詳しく").ID)
			}
			for _, m := range direct.models {
				if m == "auto" || m == "fixed" {
					t.Fatal("proxy route leaked into provider calls")
				}
			}
		})
	}
}
