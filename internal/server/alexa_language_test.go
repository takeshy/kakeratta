package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/alexa"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestAlexaEnglishLocales(t *testing.T) {
	for _, locale := range []string{"en-US", "en-GB", "en-CA", "en-AU", "en-IN"} {
		t.Run(locale, func(t *testing.T) {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s := New(Options{Store: st, Catalogue: persona.NewCatalogue(st), Passive: true})
			t.Cleanup(s.Halt)
			token := s.newAlexaToken()
			data, _ := json.Marshal(alexaCredentials{Access: alexaHash(token), AccessUntil: time.Now().Add(time.Hour)})
			if _, err := st.SwapSocialValue(context.Background(), alexaCredential, "", store.SocialValue{Data: data}); err != nil {
				t.Fatal(err)
			}
			e := alexa.Envelope{}
			e.Request.Locale, e.Request.ID, e.Request.Timestamp = locale, "english-note", time.Now()
			e.Context.System.User.AccessToken = token
			r := httptest.NewRequest("POST", "/api/alexa", nil)
			invoke := func(intent string) string {
				e.Request.Type, e.Request.Intent.Name = "IntentRequest", intent
				w := httptest.NewRecorder()
				s.handleAlexa(w, r, e)
				var body struct {
					SessionAttributes map[string]string
					Response          struct {
						OutputSpeech struct{ Type, Text string }
						Reprompt     struct{ OutputSpeech struct{ Text string } }
					}
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Response.OutputSpeech.Type != "PlainText" {
					t.Fatalf("unexpected speech type: %s", w.Body)
				}
				e.Session.Attributes = body.SessionAttributes
				if body.Response.Reprompt.OutputSpeech.Text != "" && body.Response.Reprompt.OutputSpeech.Text != "Go ahead." && body.Response.Reprompt.OutputSpeech.Text != "Say yes to post, or no to try again." {
					t.Fatalf("non-English reprompt: %s", w.Body)
				}
				return body.Response.OutputSpeech.Text
			}
			for _, intent := range []string{"AMAZON.HelpIntent"} {
				if got := invoke(intent); !strings.Contains(got, "Welcome to Kakeratta") {
					t.Fatal(got)
				}
			}
			if got := invoke("ReadRepliesIntent"); !strings.Contains(got, "no persona replies") {
				t.Fatal(got)
			}
			if got := invoke("ReadSchedulesIntent"); !strings.Contains(got, "no upcoming") {
				t.Fatal(got)
			}
			if got := invoke("ThrowKakeraIntent"); !strings.Contains(got, "What would you like to save?") {
				t.Fatal(got)
			}
			e.Request.Intent.Slots = map[string]struct {
				Value string `json:"value"`
			}{"text": {Value: "I want to go for a walk tomorrow"}}
			if got := invoke("ThrowKakeraIntent"); !strings.Contains(got, "Shall I post it?") {
				t.Fatal(got)
			}
			if got := invoke("AMAZON.YesIntent"); got != "Posted." {
				t.Fatal(got)
			}

			entries, err := st.Timeline()
			if err != nil || len(entries) != 1 || entries[0].Language != "en" {
				t.Fatalf("English post not saved exactly once: %+v, %v", entries, err)
			}
			root := entries[0].ID
			if err := st.Append(timeline.Entry{ID: "answer", Root: root, Kind: timeline.KindRemark, Speaker: "custom-persona", Text: "**Enjoy** your walk!", At: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if got := invoke("ReadRepliesIntent"); !strings.Contains(got, "From your persona. Enjoy your walk!") {
				t.Fatal(got)
			}
			// Fix a future instant near UTC midnight: the account zone must change
			// both calendar date and time; the locale must never imply its own zone.
			at := time.Date(time.Now().Year()+1, 7, 2, 1, 5, 0, 0, time.UTC)
			if _, err := st.SaveSchedule(store.Schedule{Skill: "custom-persona", At: at, Note: "Go for a walk"}); err != nil {
				t.Fatal(err)
			}
			settings := st.Settings()
			settings.TimeZone = "America/Los_Angeles"
			if err := st.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			got := invoke("ReadSchedulesIntent")
			date := "1 July"
			if locale == "en-US" {
				date = "July 1"
			}
			if !strings.Contains(got, date) || !strings.Contains(got, "6:05 PM") || !strings.Contains(got, "America/Los_Angeles") || !strings.Contains(got, "Go for a walk") {
				t.Fatal(got)
			}
			settings.TimeZone = ""
			if err := st.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			got = invoke("ReadSchedulesIntent")
			if !strings.Contains(got, "Times are in UTC.") || !strings.Contains(got, "1:05 AM") {
				t.Fatal(got)
			}
			e.Context.System.User.AccessToken = "invalid"
			if got := invoke("ReadRepliesIntent"); !strings.Contains(got, "Please link your Kakeratta account again") {
				t.Fatal(got)
			}
		})
	}
}

func TestAlexaSpeechFormattingLocales(t *testing.T) {
	en := alexaVoice("en-GB")
	if got := en.speechText("<html><body>private markup</body></html>", 700); got != "This reply includes a visual. Please view it in the app." {
		t.Fatal(got)
	}
	if got := en.speechText(strings.Repeat("é", 701), 700); !strings.HasSuffix(got, ". You can read the rest in the app.") || strings.Contains(got, "�") {
		t.Fatal("truncation did not preserve Unicode and English notice")
	}
	// Existing records retain their language; read-aloud makes no translation call.
	if got := en.speechText("日本語で保存した返事", 700); got != "日本語で保存した返事" {
		t.Fatal(got)
	}
	for _, locale := range []string{"", "ja-JP", "fr-FR"} {
		v := alexaVoice(locale)
		if v.language() != "ja" || v.defaultZone() != "Asia/Tokyo" {
			t.Fatalf("unexpected fallback for %q", locale)
		}
	}
}
