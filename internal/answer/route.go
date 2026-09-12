package answer

import (
	"context"
	"strings"

	"github.com/takeshy/dokking/internal/llm"
)

// Routing is the app's job, not the reader's.
//
// The paths stay separated - entities, retrieval, the text around the reader -
// because they are different sources with different precision. Which of them a
// question needs is something the question itself says, so asking the reader to
// classify their own question first is work the app should have done.
//
// The rules come first because they are instant, free and predictable. A model
// is consulted only when the wording gives nothing away, and even then it may
// only pick from the same list.
var routeMarkers = []struct {
	mode    Mode
	markers []string
}{
	{ModeReview, []string{
		"振り返", "ふりかえ", "伏線", "総括", "読み終", "読了", "全体を通して", "通読",
		"looking back", "in hindsight", "foreshadow",
	}},
	{ModeScene, []string{
		"どこ", "どのあたり", "どの辺", "場面", "シーン", "くだり", "出てきた", "書いてあった",
		"あったっけ", "どこだっけ", "戻りたい", "探して", "where", "scene", "passage", "find the",
	}},
	{ModeRecap, []string{
		"あらすじ", "ここまで", "これまで", "要約", "まとめて", "思い出させ", "おさらい", "振り出し",
		"recap", "summary", "so far", "remind me what",
	}},
	{ModePerson, []string{
		"誰", "だれ", "何者", "どんな人", "正体", "どういう人", "人物", "関係は", "何をした人",
		"who is", "who was", "who's", "character",
	}},
}

// Route picks the path from the wording alone. It reports whether the wording
// actually decided it, so the caller knows when it is worth asking a model.
func Route(question string) (Mode, bool) {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		// Nothing typed is the one unambiguous request: where was I?
		return ModeRecap, true
	}
	lowered := strings.ToLower(trimmed)
	for _, rule := range routeMarkers {
		for _, marker := range rule.markers {
			if strings.Contains(lowered, marker) {
				return rule.mode, true
			}
		}
	}
	// A bare name with no question around it is someone the reader lost track
	// of: "海野" means "who is Unno again?".
	if len([]rune(trimmed)) <= 12 && !strings.ContainsAny(trimmed, "？?。、") {
		return ModePerson, true
	}
	return ModeCurrent, false
}

const routePrompt = `読書アプリで、読者の質問をどの経路で答えるか選びます。次のどれか1語だけを返してください。

person  … 登場人物や用語について（誰、どんな人、何のこと）
scene   … 本文のどこに何が書いてあったかを探す（うろ覚えの場面）
recap   … ここまでの流れを思い出したい
review  … 読み終えた本の全体を振り返る
current … いま読んでいるところについての反応や疑問

語だけを返し、説明を書かないこと。`

// classify asks the light model which path fits, and falls back to the rule's
// answer on any trouble: a routing question must never block an answer.
func classify(ctx context.Context, deps Deps, question string, fallback Mode) Mode {
	model := deps.Settings.LightModel
	if model == "" || deps.Client == nil || deps.Client.BaseURL == "" {
		return fallback
	}
	reply, err := deps.Client.Chat(ctx, llm.Request{
		Model: model,
		Messages: []llm.Message{
			{Role: "system", Content: routePrompt},
			{Role: "user", Content: question},
		},
	})
	if err != nil {
		return fallback
	}
	switch word := strings.ToLower(strings.Trim(strings.TrimSpace(reply), ".。\"'` ")); Mode(word) {
	case ModePerson, ModeScene, ModeRecap, ModeReview, ModeCurrent:
		return Mode(word)
	}
	return fallback
}
