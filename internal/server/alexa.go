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

func (v alexaVoice) say(w http.ResponseWriter, text string, end bool, attrs map[string]string, link bool, directives ...map[string]any) {
	response := map[string]any{"shouldEndSession": end}
	if text != "" {
		response["outputSpeech"] = map[string]string{"type": "PlainText", "text": v.text(text)}
	}
	if !end {
		prompt := "どうぞ。"
		if attrs["draftText"] != "" {
			prompt = "投稿してよければ『はい』、言い直すなら『いいえ』と言ってください。"
		}
		response["reprompt"] = map[string]any{"outputSpeech": map[string]string{"type": "PlainText", "text": v.text(prompt)}}
	}
	if len(directives) > 0 {
		response["directives"] = directives
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
	if err := s.checkAlexaJudge(r.Context(), credentials.JudgeUID, false); err != nil {
		voice.say(w, "Review access is unavailable or has expired. Please contact the developer.", true, nil, false)
		return
	}
	attrs := map[string]string{}
	// Signed session attributes carry a draft, never authority over another workspace.
	for _, key := range []string{"postId", "replyId", "draftText", "draftId", "draftReply", "collectIntent"} {
		if value := e.Session.Attributes[key]; value != "" {
			attrs[key] = value
		}
	}
	if e.Request.Type == "LaunchRequest" {
		voice.say(w, "かけらったです。どうしますか。", false, nil, false)
		return
	}
	if e.Request.Type != "IntentRequest" {
		voice.say(w, "もう一度お願いします。", false, attrs, false)
		return
	}
	switch e.Request.Intent.Name {
	case "ThrowKakeraIntent", "ReplyKakeraIntent":
		intent := e.Request.Intent.Name
		clearAlexaDraft(attrs)
		attrs["collectIntent"] = intent
		if intent == "ReplyKakeraIntent" {
			if _, ok := s.alexaReplyTarget(attrs["replyId"]); !ok {
				delete(attrs, "collectIntent")
				voice.say(w, "先に『返事を聞いて』で、返信する返事を聞いてください。", false, attrs, false)
				return
			}
		}
		text := strings.TrimSpace(e.Request.Intent.Slots["text"].Value)
		if text == "" {
			voice.elicit(w, intent, attrs)
			return
		}
		if len([]rune(text)) > 2000 || s.hasMultipleMentionedPeople(r.Context(), text) {
			voice.say(w, "短いかけらを一つずつ話してください。担当の指名は一人までです。", false, attrs, false)
			return
		}
		attrs["draftText"], attrs["draftId"] = text, "alexa-"+alexaHash(e.Request.ID)
		if intent == "ReplyKakeraIntent" {
			attrs["draftReply"] = attrs["replyId"]
		}
		delete(attrs, "collectIntent")
		voice.confirmDraft(w, attrs)
	case "AMAZON.YesIntent":
		s.confirmAlexaPost(w, r, e, attrs, credentials.JudgeUID, voice)
	case "AMAZON.NoIntent", "ReviseKakeraIntent":
		intent := "ThrowKakeraIntent"
		if attrs["draftReply"] != "" || attrs["collectIntent"] == "ReplyKakeraIntent" {
			intent = "ReplyKakeraIntent"
		}
		clearAlexaDraft(attrs)
		attrs["collectIntent"] = intent
		voice.elicit(w, intent, attrs)
	case "ReadRepliesIntent":
		clearAlexaDraft(attrs)
		s.alexaReplies(w, r, attrs, voice)
	case "ReadSchedulesIntent":
		clearAlexaDraft(attrs)
		s.alexaSchedules(w, r, attrs, voice)
	case "AMAZON.HelpIntent":
		voice.say(w, alexaHelp, false, attrs, false)
	default:
		if attrs["draftText"] != "" {
			voice.confirmDraft(w, attrs)
		} else if attrs["collectIntent"] != "" {
			voice.elicit(w, attrs["collectIntent"], attrs)
		} else {
			voice.say(w, "聞き取れませんでした。もう一度お願いします。", false, attrs, false)
		}
	}
}

func clearAlexaDraft(attrs map[string]string) {
	for _, key := range []string{"draftText", "draftId", "draftReply", "collectIntent"} {
		delete(attrs, key)
	}
}

func (v alexaVoice) elicit(w http.ResponseWriter, intent string, attrs map[string]string) {
	prompt := "メモの内容をどうぞ。"
	if intent == "ReplyKakeraIntent" {
		prompt = "返信の内容をどうぞ。"
	}
	v.say(w, prompt, false, attrs, false, map[string]any{
		"type": "Dialog.ElicitSlot", "slotToElicit": "text",
		"updatedIntent": map[string]any{"name": intent, "confirmationStatus": "NONE", "slots": map[string]any{
			"text": map[string]string{"name": "text", "confirmationStatus": "NONE"},
		}},
	})
}

func (v alexaVoice) confirmDraft(w http.ResponseWriter, attrs map[string]string) {
	v.say(w, fmt.Sprintf(v.text("『%s』。この内容で投稿しますか。はい、または、いいえで答えてください。"), attrs["draftText"]), false, attrs, false)
}

func (s *Server) alexaReplyTarget(id string) (timeline.Entry, bool) {
	parent, ok := s.store.Entry(id)
	if !ok || parent.Deleted || parent.Speaker == timeline.User || parent.Speaker == timeline.App {
		return timeline.Entry{}, false
	}
	root, ok := s.store.Entry(timeline.RootOf(parent))
	return parent, ok && !root.Deleted
}

func (s *Server) confirmAlexaPost(w http.ResponseWriter, r *http.Request, e alexa.Envelope, attrs map[string]string, judgeUID string, voice alexaVoice) {
	text, id := attrs["draftText"], attrs["draftId"]
	if strings.TrimSpace(text) == "" || len([]rune(text)) > 2000 || !strings.HasPrefix(id, "alexa-") || len(id) != 70 {
		clearAlexaDraft(attrs)
		voice.say(w, "確認待ちの投稿はありません。『メモして』か『返信して』と言ってください。", false, attrs, false)
		return
	}
	entry := timeline.Entry{ID: id, Root: id, Text: text, Kind: timeline.KindPost, Speaker: timeline.User, At: e.Request.Timestamp, Language: voice.language(), RequestHash: alexaHash(text + "\x00" + attrs["draftReply"])}
	if target := attrs["draftReply"]; target != "" {
		parent, ok := s.alexaReplyTarget(target)
		if !ok {
			clearAlexaDraft(attrs)
			voice.say(w, "返信先が見つかりません。もう一度『返事を聞いて』と言ってください。", false, attrs, false)
			return
		}
		entry.Kind, entry.Root, entry.ReplyTo = timeline.KindReply, timeline.RootOf(parent), parent.ID
	}
	// A retried confirmation can acknowledge an already accepted draft without charging again.
	if _, exists := s.store.Entry(id); !exists {
		if err := s.checkAlexaJudge(r.Context(), judgeUID, true); err != nil {
			voice.say(w, "Review access is unavailable or its usage limit has been reached. Please contact the developer.", true, nil, false)
			return
		}
	}
	if _, _, err := s.acceptEntry(entry, nil); err != nil {
		voice.say(w, "受け付けを確認できませんでした。アプリで確認してください。", true, nil, false)
		return
	}
	clearAlexaDraft(attrs)
	attrs["postId"] = entry.Root
	delete(attrs, "replyId")
	voice.say(w, "投稿しました。", false, attrs, false)
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
		delete(attrs, "replyId")
		voice.say(w, "最近のかけらには、まだ担当からの返事がありません。少し待ってからもう一度聞いてください。", false, attrs, false)
		return
	}
	entry := entries[0]
	name := s.alexaNames(r.Context())[entry.Speaker]
	if entry.Speaker == timeline.Agent {
		if name = s.activeMCPName(); name == "" {
			name = voice.text("外部エージェント")
		}
	}
	if name == "" {
		name = voice.text("担当")
	}
	attrs["replyId"] = entry.ID
	voice.say(w, fmt.Sprintf(voice.text("%sから。%s"), voice.speechText(name, 80), voice.speechText(entry.Text, 700))+" "+voice.text("『返信して』で、この返事に返信できます。"), false, attrs, false)
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
