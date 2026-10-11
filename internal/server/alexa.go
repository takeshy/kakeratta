package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/alexa"
	"github.com/takeshy/kakeratta/internal/social"
	"github.com/takeshy/kakeratta/internal/timeline"
)

const alexaHelp = "かけらったです。『メモして、明日は散歩に行きたい』でかけらを投げられます。『返事を聞いて』『予定を教えて』とも言えます。"

func (v alexaVoice) say(w http.ResponseWriter, text string, end bool, attrs map[string]string, link bool) {
	response := map[string]any{"shouldEndSession": end}
	if text != "" {
		response["outputSpeech"] = map[string]string{"type": "PlainText", "text": v.text(text)}
	}
	if !end {
		response["reprompt"] = map[string]any{"outputSpeech": map[string]string{"type": "PlainText", "text": v.text("『メモして』に続けて内容を話すか、『返事を聞いて』『予定を教えて』と言ってください。")}}
	}
	if link {
		response["card"] = map[string]string{"type": "LinkAccount"}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"version": "1.0", "sessionAttributes": attrs, "response": response})
}

func (g *Gateway) serveAlexa(w http.ResponseWriter, r *http.Request) {
	if !g.options.Alexa.Enabled() {
		notFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	envelope, err := g.options.Alexa.Read(w, r)
	if err != nil {
		fail(w, 400, "invalid Alexa request")
		return
	}
	voice := alexaVoice(envelope.Request.Locale)
	if envelope.Request.Type == "SessionEndedRequest" {
		voice.say(w, "", true, nil, false)
		return
	}
	if envelope.Request.Type == "IntentRequest" {
		switch envelope.Request.Intent.Name {
		case "AMAZON.StopIntent", "AMAZON.CancelIntent":
			voice.say(w, "またいつでも話しかけてください。", true, nil, false)
			return
		}
	}
	tenant := g.existingAlexaWorkspace(r, envelope.Token())
	if tenant == nil {
		voice.say(w, "Alexaアプリで、かけらったのアカウントを連携してください。", true, nil, true)
		return
	}
	tenant.handleAlexa(w, r, envelope)
}

func (s *Server) handleAlexa(w http.ResponseWriter, r *http.Request, e alexa.Envelope) {
	voice := alexaVoice(e.Request.Locale)
	v, err := s.store.SocialValue(r.Context(), alexaCredential)
	if err != nil {
		voice.say(w, "今は接続できません。少し待ってからもう一度お試しください。", true, nil, false)
		return
	}
	var credentials alexaCredentials
	if json.Unmarshal(v.Data, &credentials) != nil || alexaTokenWorkspace(e.Token()) != s.mcpWorkspace() || !alexaMatches(e.Token(), credentials.Access) || !time.Now().Before(credentials.AccessUntil) {
		voice.say(w, "Alexaアプリで、かけらったのアカウントを連携し直してください。", true, nil, true)
		return
	}
	attrs := map[string]string{}
	// Session attributes are signed by Alexa, but never authorize other workspaces.
	if id := e.Session.Attributes["postId"]; id != "" {
		attrs["postId"] = id
	}
	if e.Request.Type == "LaunchRequest" {
		voice.say(w, alexaHelp, false, attrs, false)
		return
	}
	if e.Request.Type != "IntentRequest" {
		voice.say(w, alexaHelp, false, attrs, false)
		return
	}
	switch e.Request.Intent.Name {
	case "ThrowKakeraIntent":
		text := strings.TrimSpace(e.Request.Intent.Slots["text"].Value)
		if text == "" {
			voice.say(w, "『メモして』に続けて、かけらの内容を話してください。", false, attrs, false)
			return
		}
		if len([]rune(text)) > 2000 || s.hasMultipleMentionedPeople(r.Context(), text) {
			voice.say(w, "短いかけらを一つずつ話してください。担当の指名は一人までです。", false, attrs, false)
			return
		}
		// A stable request ID makes retries across processes use CreateEntry's
		// existing atomic entry + routing acceptance, without fetching spoken URLs.
		id := "alexa-" + alexaHash(e.Request.ID)
		entry := timeline.Entry{ID: id, Root: id, Text: text, Kind: timeline.KindPost, Speaker: timeline.User, At: e.Request.Timestamp, Language: voice.language(), RequestHash: alexaHash(text)}
		if _, _, err := s.acceptEntry(entry, nil); err != nil {
			voice.say(w, "受け付けを確認できませんでした。アプリで確認してください。", true, attrs, false)
			return
		}
		attrs["postId"] = id
		voice.say(w, "かけらを受け付けました。担当から返事が届いたら、『返事を聞いて』で聞けます。", false, attrs, false)
	case "ReadRepliesIntent":
		s.alexaReplies(w, r, attrs, voice)
	case "ReadSchedulesIntent":
		s.alexaSchedules(w, r, attrs, voice)
	default:
		voice.say(w, alexaHelp, false, attrs, false)
	}
}

func (v alexaVoice) speechText(text string, limit int) string {
	if social.HTMLDocument(text) != "" {
		return v.text("図や画像を含む返事です。アプリで確認してください。")
	}
	text, _ = social.FromMarkdown(text)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + v.text("。続きはアプリで確認してください。")
	}
	return text
}

func (s *Server) alexaNames(ctx context.Context) map[string]string {
	names := map[string]string{}
	for _, skill := range s.catalogue.All(ctx) {
		names[skill.ID] = skill.Name
	}
	return names
}

func (s *Server) alexaReplies(w http.ResponseWriter, r *http.Request, attrs map[string]string, voice alexaVoice) {
	page, err := s.store.TimelinePage(r.Context(), 100, "")
	if err != nil {
		voice.say(w, "返事を読み込めませんでした。少し待ってからお試しください。", true, attrs, false)
		return
	}
	entries := []timeline.Entry{}
	for _, entry := range page.Entries {
		if entry.Deleted || entry.Speaker == timeline.User || entry.Speaker == timeline.App || strings.TrimSpace(entry.Text) == "" {
			continue
		}
		if attrs["postId"] != "" && entry.Root != attrs["postId"] {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].At.Equal(entries[j].At) {
			return entries[i].ID > entries[j].ID
		}
		return entries[i].At.After(entries[j].At)
	})
	if len(entries) == 0 {
		voice.say(w, "最近のかけらには、まだ担当からの返事がありません。少し待ってからもう一度聞いてください。", false, attrs, false)
		return
	}
	names := s.alexaNames(r.Context())
	lines := []string{voice.text("最近のかけらに届いた返事です。")}
	for i, entry := range entries {
		if i == 3 {
			lines = append(lines, voice.text("ほかの返事はアプリで確認できます。"))
			break
		}
		name := names[entry.Speaker]
		if name == "" {
			name = voice.text("担当")
		}
		lines = append(lines, fmt.Sprintf(voice.text("%sから。%s"), voice.speechText(name, 80), voice.speechText(entry.Text, 700)))
	}
	voice.say(w, strings.Join(lines, " "), false, attrs, false)
}

func (s *Server) alexaSchedules(w http.ResponseWriter, r *http.Request, attrs map[string]string, voice alexaVoice) {
	rows, err := s.store.Schedules("")
	if err != nil {
		voice.say(w, "予定を読み込めませんでした。少し待ってからお試しください。", true, attrs, false)
		return
	}
	zone := s.store.Settings().TimeZone
	loc, err := time.LoadLocation(zone)
	if zone == "" || zone == "Local" || err != nil {
		zone = voice.defaultZone()
		loc, _ = time.LoadLocation(zone)
	}
	names := s.alexaNames(r.Context())
	lines := []string{}
	now := time.Now()
	sort.Slice(rows, func(i, j int) bool { return rows[i].At.Before(rows[j].At) })
	for _, row := range rows {
		if row.At.Before(now) {
			continue
		}
		if len(lines) == 5 {
			lines = append(lines, voice.text("ほかの予定はアプリで確認できます。"))
			break
		}
		name := names[row.Skill]
		if name == "" {
			name = voice.text("担当")
		}
		lines = append(lines, fmt.Sprintf(voice.text("%s、%s。%s。"), voice.dateTime(row.At.In(loc)), voice.speechText(name, 80), voice.speechText(row.Note, 200)))
	}
	if len(lines) == 0 {
		voice.say(w, "これからの予定は登録されていません。", false, attrs, false)
		return
	}
	voice.say(w, fmt.Sprintf(voice.text("これからの予定です。時刻は%sです。 "), zone)+strings.Join(lines, " "), false, attrs, false)
}
