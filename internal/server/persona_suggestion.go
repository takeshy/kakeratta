package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// suggestedPrompt follows builderPrompt when the persona is drafted from a
// check-in's suggestion: nobody is there to answer a question, and what of the
// suggesting persona's memory belongs to the new one is picked by id, so it is
// moved word for word rather than rewritten.
const suggestedPrompt = `

## 記憶の整理から提案された新しい担当
この依頼は、既存の担当（元の担当）の記憶の整理で「新しい担当に分けるとよい主題」が見つかり、ユーザーがその提案の「作る」ボタンを押したものです。質問せず、必ず完成案を返してください。人物の担当にはせず、checkIn と basicInfo は付けません。memoryEnabled は true です。
主題・理由・元の担当の定義と記憶はユーザーの発言にあります。主題に沿って id・name・icon・domain・tone・memory・instructions を補います。domain は元の担当の担当条件と重ならないように、主題の話題そのものを書きます。tone は元の担当に近い親しみやすさにします。
あわせて、元の担当の記憶から新しい担当に移すものを選びます。選んだ事実と記録は元の担当の記憶から消え、新しい担当だけが持ちます。"facts" に事実のID（f_ で始まる）、"records" に記録のID（行の [ ] の中）を並べます。主題に関係するものだけを選び、元の担当の領域にも関わるものは移しません。なければ空の配列にします。渡されていないIDを作らないでください。
主題の投稿が元の担当にも届き続けないよう、"sourceDomain" に元の担当の担当条件を書き直して返します。元の条件の対象はそのまま残し、新しい担当の主題を除くことを1文だけ加えます（例：「料理・食事の写真や話。ただし、レシピや料理の作り方の話は除く。」）。元の担当条件と同じ言語で書きます。
返すJSON: {"message":"完成案の短い説明","placeholder":"","draft":skillオブジェクト,"facts":[],"records":[],"sourceDomain":"元の担当の新しい担当条件"}`

// suggestedDraft is a persona drafted from a suggestion, with what of the
// suggesting persona's memory it takes over.
type suggestedDraft struct {
	Message string         `json:"message"`
	Draft   *persona.Skill `json:"draft"`
	Facts   []string       `json:"facts"`
	Records []string       `json:"records"`
	// SourceDomain is the suggesting persona's condition with the new
	// persona's subject excluded, so a post on it reaches the new one alone.
	SourceDomain string `json:"sourceDomain"`
}

// answerSuggestion answers a check-in post that suggested a new persona, as
// the user chose with the buttons under it. "decline" only records the answer.
// "create" drafts the persona from the subject, the reason and the suggesting
// persona, installs and activates it, and moves the facts and records the
// draft picked from the suggesting persona's memory into its own. The
// suggesting persona's condition is rewritten to leave the subject out, so a
// post on it is routed to the new persona and not to both. A step that fails
// undoes those before it (undoSuggested).
func (s *Server) answerSuggestion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Choice   string `json:"choice"`
		Language string `json:"language"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, "答えを選んでください")
		return
	}
	id := r.PathValue("id")
	if body.Choice == "decline" {
		entry, err := s.runner.AnswerSuggestion(id, timeline.SuggestionDeclined, "")
		if s.suggestionFailed(w, err) {
			return
		}
		s.publishEvent("entry", entry)
		writeJSON(w, http.StatusOK, map[string]any{"entry": entry})
		return
	}
	if body.Choice != "create" {
		fail(w, http.StatusBadRequest, "答えは create か decline です")
		return
	}
	held, release, err := s.runner.LockSuggestion(r.Context(), id)
	if err != nil {
		fail(w, http.StatusConflict, "ほかの操作が終わってからもう一度押してください")
		return
	}
	defer release()
	entry, err := s.runner.SuggestionAsked(id)
	if s.suggestionFailed(w, err) {
		return
	}
	source, ok := s.catalogue.Find(held, entry.Speaker)
	if !ok {
		fail(w, http.StatusNotFound, "提案した担当が見つかりません")
		return
	}
	drafted, err := s.draftSuggested(held, *entry.Suggestion, source, body.Language)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	// Making it is three writes - the new persona, the suggesting persona's
	// narrowed condition, the memory moved between them - and the user sees
	// all of them or none: a step that fails undoes the ones before it, the
	// post still waits for an answer, and the button can be pressed again.
	raw, _ := json.Marshal(drafted.Draft)
	saved, err := s.catalogue.Install(raw, s.knownTool)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	narrowed, err := s.excludeSubject(held, source, drafted.SourceDomain, entry.Suggestion.Topic)
	if err != nil {
		s.undoSuggested(held, saved.ID, nil)
		fail(w, http.StatusInternalServerError, "新しい担当を作れませんでした。もう一度押してください。")
		return
	}
	facts, records, err := s.runner.MoveMemory(held, source.ID, saved.ID, drafted.Facts, drafted.Records)
	if err != nil {
		s.undoSuggested(held, saved.ID, &source)
		fail(w, http.StatusInternalServerError, "新しい担当を作れませんでした。もう一度押してください。")
		return
	}
	made := added{Skill: saved, Active: s.activatePersona(saved.ID) == nil}
	entry, err = s.runner.AnswerSuggestion(id, timeline.SuggestionCreated, saved.ID)
	if err != nil {
		// Everything is made; only the answer on the post is missing, and
		// undoing would move the memory back for a mark.
		log.Printf("suggested persona %s answer on %s: %v", saved.ID, id, err)
		fail(w, http.StatusInternalServerError, "担当は作りましたが、投稿に答えを残せませんでした")
		return
	}
	s.publishEvent("entry", entry)
	writeJSON(w, http.StatusOK, map[string]any{"entry": entry, "persona": made, "facts": facts, "records": records, "source": narrowed})
}

func (s *Server) suggestionFailed(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, persona.ErrNoSuggestion):
		fail(w, http.StatusConflict, err.Error())
	default:
		fail(w, http.StatusInternalServerError, err.Error())
	}
	return true
}

// draftSuggested asks the persona builder for the suggested persona, once,
// with no conversation: the subject and the suggesting persona are all it has.
func (s *Server) draftSuggested(ctx context.Context, suggestion timeline.PersonaSuggestion, source persona.Skill, language string) (suggestedDraft, error) {
	skills := s.catalogue.Snapshot(ctx)
	ids := []string{}
	for _, skill := range skills.All() {
		ids = append(ids, skill.ID)
	}
	setting, _ := json.Marshal(map[string]any{"existing": nil, "usedIDs": ids, "availableTools": tools.New(s.tools).Names(), "language": language})
	from, _ := json.Marshal(map[string]any{"id": source.ID, "name": source.Name, "domain": source.Domain, "memory": source.Memory, "tools": source.Tools})
	user := "主題: " + suggestion.Topic + "\n理由: " + suggestion.Reason +
		"\n\n元の担当:\n" + string(from) + "\n\n元の担当の記憶:\n" + s.runner.SuggestionMemory(source.ID)
	client, settings := s.client()
	reply, err := client.Chat(ctx, llm.Request{Purpose: "persona_builder", Model: settings.HighModel, JSON: true, Temperature: 0.4, Messages: []llm.Message{
		{Role: "system", Content: builderPrompt + "\n設定データ:\n" + string(setting) + suggestedPrompt},
		{Role: "user", Content: user},
	}})
	if err != nil {
		return suggestedDraft{}, err
	}
	var result suggestedDraft
	if err := llm.DecodeJSON(reply, &result); err != nil || result.Draft == nil {
		return suggestedDraft{}, errors.New("AIの応答を読めませんでした。もう一度押してください。")
	}
	draft := result.Draft
	draft.Kind, draft.Aliases, draft.Keywords, draft.BasicInfo = "", nil, nil, ""
	draft.MemoryEnabled = nil
	draft.Avatar, draft.Builtin, draft.Customized = "", false, false
	draft.TidyTerms()
	draft.ID = freeID(draft.ID, func(id string) bool { return slices.Contains(ids, id) })
	if err := persona.Validate(*draft, s.knownTool); err != nil {
		return suggestedDraft{}, err
	}
	return result, nil
}

// undoSuggested takes back a persona made from a suggestion when a later
// step failed: the persona and any memory written for it go, and the
// suggesting persona's condition is put back when it was changed. What cannot
// be undone is logged; the user is told the persona was not made.
func (s *Server) undoSuggested(ctx context.Context, made string, source *persona.Skill) {
	if source != nil {
		if err := s.catalogue.Restore(ctx, *source, s.knownTool); err != nil {
			log.Printf("suggested persona %s: restoring %s: %v", made, source.ID, err)
		}
	}
	if err := s.store.ForgetMemory(made); err != nil {
		log.Printf("suggested persona %s: forgetting its memory: %v", made, err)
	}
	if err := s.catalogue.Uninstall(made); err != nil {
		log.Printf("suggested persona %s: removing it: %v", made, err)
	}
}

// excludeSubject saves the suggesting persona with its condition narrowed:
// the drafted one, or when none usable came back, its own with the subject
// left out.
func (s *Server) excludeSubject(ctx context.Context, source persona.Skill, domain, topic string) (persona.Skill, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" || domain == strings.TrimSpace(source.Domain) || len([]rune(domain)) > maxDomainRunes {
		domain = strings.TrimSpace(source.Domain) + excludedLine(source.Domain, topic)
	}
	source.Domain = domain
	raw, _ := json.Marshal(source)
	return s.catalogue.Update(ctx, source.ID, raw, s.knownTool)
}

// maxDomainRunes is the longest condition a persona can be saved with.
const maxDomainRunes = 2000

var japaneseText = regexp.MustCompile(`[\p{Hiragana}\p{Katakana}\p{Han}]`)

// excludedLine is the sentence that leaves topic out of a condition written in
// the language of domain.
func excludedLine(domain, topic string) string {
	if japaneseText.MatchString(domain) {
		return "ただし、" + topic + "の話は除く。"
	}
	return " Excludes " + topic + "."
}

// freeID is id, or id with a number after it when another persona has it.
func freeID(id string, taken func(string) bool) string {
	id = strings.TrimSpace(id)
	if !taken(id) {
		return id
	}
	for n := 2; ; n++ {
		next := id + "-" + strconv.Itoa(n)
		if !taken(next) {
			return next
		}
	}
}
