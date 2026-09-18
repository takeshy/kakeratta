package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

const builderPrompt = `あなたは対話でAIペルソナを設計するアシスタントです。ユーザーの言語で話してください。
最初の「どんなことの担当を作りたい？」への回答で担当分野が少しでも分かれば、追加質問せず、その場で保存できる完成案を返してください。「ワイン」「読書」など短い回答でも十分です。
まず使い始めて、あとで改善できることを優先します。名前、口調、担当範囲、覚えること、返事のタイミングは、ユーザーの希望に沿う無難な初期案をあなたが補ってください。これらを順番に質問したり、似た内容を言い換えて確認したりしないでください。
担当分野が全く分からず案を作れない場合だけ、具体例を添えた短い質問を1回まで許可します。その回答後は、不明点を補って必ず完成案を出してください。「おまかせ」「まず作って」と言われたら確認なしで完成案を出します。
既存のペルソナの修正でも、依頼内容をそのまま反映した完成案をすぐ返し、依頼のない内容とIDを保持してください。追加修正も同様で、確認のための質問を挟まないでください。
messageは補った点を必要に応じて一言添える程度の短い説明にし、「使ってからAIで修正できます」と案内してください。完成案のあとに追加質問を付けないでください。専門用語やJSON入力を要求しないでください。
必ずJSONで {"message":"完成案の短い説明", "placeholder":"追加修正の具体例", "draft":完全なskillオブジェクト} を返します。例外として、担当分野を初めて確認する場合だけdraftをnullにします。
placeholderはユーザーが次に入力する回答の短い具体例です。必ずユーザーの言語で「例：」から始め、今回の質問と会話の担当分野に合わせて毎回作り直してください。質問文そのものや「回答してください」のような抽象的な案内にしないでください。
例えばワイン担当の案への修正なら「例：飲んだワインの名前と、好きだった理由を覚えてほしい。」、担当分野の確認なら「例：読んだ本の感想を覚える担当。」のようにします。
完成案を返すときのplaceholderは、その完成案に対する追加修正の具体例にしてください。例は選択肢の制約ではなく入力のヒントです。本文と同じく専門用語を避け、120文字程度までにしてください。
skill: {"id":"英小文字・数字・ハイフンで40文字以内", "name":"40文字以内", "icon":"絵文字1つ", "domain":"短い担当条件、原則1〜2文・日本語40〜100文字程度、上限2000文字", "tone":"2000文字以内", "instructions":"8000文字以内", "model":"lowまたはmiddleまたはhigh", "tools":[]}。
domainは振り分け用の「担当条件」です。どんな投稿が対象かを肯定形で1〜2文、日本語なら40〜100文字程度にまとめます。短さのためにユーザーが明示した条件を落としたり、担当範囲を変えたりしないでください。
対象の話題・写真・記録を簡潔に書き、依頼や質問を前提にしません。例は必要なら1つまで。除外事項の列挙、スレッドの読み方など全担当共通の説明、返事・記憶・道具の手順は入れません。
対象が複数ある場合は「または」で分け、同時に必要な条件は「かつ」で区別します。「判定する話」「一緒に考える相談」のような行動や依頼を条件にせず、投稿にある話題・写真そのものを書きます。
例：「料理・食事の写真。または食材のプリン体に関する話。」返事・記憶・道具の使い方はinstructionsに分けます。既存案も修正する担当条件はこの形に整えます。
domain、tone、instructionsはそのままユーザーに表示します。JSON、Markdown記法、type:やindex.mdなどの実装用語は本文に含めず、誰でもわかる短い自然な文章で書いてください。
instructionsには何を覚えるか、いつ記憶をまとめるか、引用と本人の意見を分けること、推測と明記すること、代表例と反例を残すこと、どんなときに返事するかを具体的に書いてください。記憶の形式自体はアプリが別に渡します。
覚える対象の種類（メモの型）を2〜4つに決めて書いてください（例: ワイン担当なら「Wine（銘柄）と Preference（好み）」）。同じ対象は型が違っても1つのメモにまとめる、と書いてください。
modelは通常middle、道具は必要なものだけ利用可能なリストから選びます。web_search以外の道具を使うならmiddleかhighにしてください。lowはメモとweb_searchと発言だけの軽い担当です。新規IDは既存IDと重複させないでください。
保存はユーザーが別の保存ボタンで行います。保存済みと発言しないでください。`

func (s *Server) buildPersona(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string        `json:"id"`
		EntryID  string        `json:"entryId"`
		Messages []llm.Message `json:"messages"`
		Language string        `json:"language"`
		// Current is the persona as it stands in the edit form, unsaved changes
		// included. The request is written against what the user is looking at,
		// so that is what the AI changes.
		Current *persona.Skill `json:"current"`
	}
	if err := readJSONLimit(r, &body, 256<<10); err != nil || len(body.Messages) == 0 {
		fail(w, 400, "会話を読めませんでした")
		return
	}
	for i, m := range body.Messages {
		expected := "user"
		if i%2 == 1 {
			expected = "assistant"
		}
		if m.Role != expected || strings.TrimSpace(m.Content) == "" {
			fail(w, 400, "会話の形式が不正です")
			return
		}
	}
	if body.Messages[len(body.Messages)-1].Role != "user" {
		fail(w, 400, "回答を入力してください")
		return
	}
	var original *persona.Skill
	if body.ID != "" {
		skill, ok := s.catalogue.Find(body.ID)
		if !ok {
			fail(w, 404, "担当が見つかりません")
			return
		}
		original = &skill
		if body.Current != nil {
			current := *body.Current
			current.ID = skill.ID
			original = &current
		}
	}
	ids := []string{}
	for _, skill := range s.catalogue.All() {
		ids = append(ids, skill.ID)
	}
	client, _ := s.client()
	context, _ := json.Marshal(map[string]any{"existing": original, "usedIDs": ids, "availableTools": tools.New(s.tools).Names(), "language": body.Language})
	messages := []llm.Message{{Role: "system", Content: builderPrompt + "\n設定データ:\n" + string(context)}}
	messages = append(messages, body.Messages...)
	if body.EntryID != "" {
		entry, ok := s.store.Entry(body.EntryID)
		if !ok || entry.Deleted {
			fail(w, http.StatusNotFound, "投稿が見つかりません")
			return
		}
		entries, err := s.store.Timeline()
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Use the same rendering as the router, including media, sources and
		// conversation context. The browser's excerpt is only for display.
		thread := timeline.Thread(entries, timeline.RootOf(entry))
		if entry.Kind == timeline.KindRemark {
			// A disputed reply is judged against the conversation before it,
			// just as in PreviewReply, rather than later user corrections.
			before := make([]timeline.Entry, 0, len(thread))
			for _, candidate := range thread {
				if candidate.ID != entry.ID && candidate.At.Before(entry.At) {
					before = append(before, candidate)
				}
			}
			thread = before
		}
		messages[len(messages)-1].Content += "\n\nスレッド全体（古い順）:\n" + timeline.Render(thread, nil, nil) +
			"\n\n今回の対象発言:\n" + entry.Render(nil, nil)
	}
	model := personaBuilderModel()
	reply, err := client.Chat(r.Context(), llm.Request{Model: model, JSON: true, Temperature: 0.4, Messages: messages})
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	var result struct {
		Message     string         `json:"message"`
		Placeholder string         `json:"placeholder"`
		Draft       *persona.Skill `json:"draft"`
	}
	if err := llm.DecodeJSON(reply, &result); err != nil || strings.TrimSpace(result.Message) == "" {
		fail(w, 502, "AIの応答を読めませんでした。もう一度送信してください。")
		return
	}
	if result.Draft != nil {
		if original != nil {
			result.Draft.ID = original.ID
		}
		result.Draft.Builtin = false
		result.Draft.Customized = false
		if err := persona.Validate(*result.Draft, s.knownTool); err != nil {
			fail(w, 502, err.Error())
			return
		}
		if original == nil {
			if _, exists := s.catalogue.Find(result.Draft.ID); exists {
				fail(w, 502, "同じIDの担当があります。別の名前を指定してください。")
				return
			}
		}
	}
	writeJSON(w, 200, result)
}

// Saving is separate from conversation: generated text never changes a persona by itself.
func (s *Server) savePersonaDraft(w http.ResponseWriter, r *http.Request) {
	var skill persona.Skill
	if err := readJSONLimit(r, &skill, 256<<10); err != nil {
		fail(w, 400, "定義を読めませんでした")
		return
	}
	id := r.PathValue("id")
	previous, exists := s.catalogue.Find(skill.ID)
	if id == "" && exists {
		fail(w, 409, "同じIDの担当があります")
		return
	}
	if id != "" && (!exists || id != skill.ID) {
		fail(w, 400, "担当のIDは変更できません")
		return
	}
	raw, _ := json.Marshal(skill)
	var saved persona.Skill
	var err error
	if id == "" {
		saved, err = s.catalogue.Install(raw, s.knownTool)
	} else {
		saved, err = s.catalogue.Update(id, raw, s.knownTool)
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	encoded, _ := json.Marshal(saved)
	if err := s.store.PersistSkill(saved.ID, encoded); err != nil {
		if exists {
			old, _ := json.Marshal(previous)
			_, _ = s.catalogue.Update(id, old, s.knownTool)
		} else {
			_ = s.catalogue.Uninstall(saved.ID)
		}
		fail(w, 500, "担当を保存できませんでした")
		return
	}
	writeJSON(w, 200, saved)
}

// personaBuilderModel writes the conditions routing depends on, so it is Ultra
// even though routing itself runs on Lightning.
func personaBuilderModel() string {
	return store.NemotronUltra
}
