package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
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
skill: {"id":"英小文字・数字・ハイフンで40文字以内", "name":"40文字以内", "icon":"絵文字1つ", "domain":"担当すること、2000文字以内", "tone":"2000文字以内", "instructions":"8000文字以内", "model":"lowまたはmiddleまたはhigh", "tools":[]}。
domainには「担当すること」だけを肯定形で書いてください。「担当しないこと」「対象外」「反応しないこと」の列挙や、その確認の質問は不要です。担当することの記述だけで投稿の振り分けに使えるよう具体的にしてください。既存案に対象外の列挙がある場合も、修正案では担当することの説明にまとめてください。
domain、tone、instructionsはそのままユーザーに表示します。JSON、Markdown記法、type:やindex.mdなどの実装用語は本文に含めず、誰でもわかる短い自然な文章で書いてください。
instructionsには何を覚えるか、いつ記憶をまとめるか、引用と本人の意見を分けること、推測と明記すること、代表例と反例を残すこと、どんなときに返事するかを具体的に書いてください。記憶の形式自体はアプリが別に渡します。
modelは通常middle、道具は必要なものだけ利用可能なリストから選びます。新規IDは既存IDと重複させないでください。
保存はユーザーが別の保存ボタンで行います。保存済みと発言しないでください。`

func (s *Server) buildPersona(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string        `json:"id"`
		Messages []llm.Message `json:"messages"`
		Language string        `json:"language"`
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
	}
	ids := []string{}
	for _, skill := range s.catalogue.All() {
		ids = append(ids, skill.ID)
	}
	client, settings := s.client()
	context, _ := json.Marshal(map[string]any{"existing": original, "usedIDs": ids, "availableTools": tools.New(settings).Names(), "language": body.Language})
	messages := []llm.Message{{Role: "system", Content: builderPrompt + "\n設定データ:\n" + string(context)}}
	messages = append(messages, body.Messages...)
	model := settings.HighModel
	if model == "" {
		model = settings.MiddleModel
	}
	if model == "" {
		model = settings.LowModel
	}
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
