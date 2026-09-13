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
最初の「どんなことの担当を作りたい？」への回答から、目的、担当範囲と対象外、口調、記憶に残したいこと、発言するタイミングを具体化します。
回答済みの内容は再質問せず、不足する重要事項を一度に1〜2問だけ質問してください。専門用語やJSON入力を要求しないでください。
十分な情報があれば完成案を返してください。おまかせと言われた部分は妥当な提案で補い説明してください。固定回数で終了せず、必要なだけ質問します。
既存のペルソナの修正では変更したい点を確認し、依頼のない内容とIDを保持してください。完成案への追加の修正も受け付けます。
必ずJSONで {"message":"質問または完成案の説明", "placeholder":"その質問への具体的な回答例", "draft":null} を返します。完成可能なときだけdraftを完全なskillオブジェクトにします。
placeholderはユーザーが次に入力する回答の短い具体例です。必ずユーザーの言語で「例：」から始め、今回の質問と会話の担当分野に合わせて毎回作り直してください。質問文そのものや「回答してください」のような抽象的な案内にしないでください。
例えば口調の質問には「例：友達のように気軽に、短めに話してほしい。」、記憶の質問には「例：飲んだワインの名前と、好きだった理由を覚えてほしい。」のようにします。複数の質問があるときはそれぞれへの短い回答例を含めてください。
完成案を返すときのplaceholderは、その完成案に対する追加修正の具体例にしてください。例は選択肢の制約ではなく入力のヒントです。本文と同じく専門用語を避け、120文字程度までにしてください。
skill: {"id":"英小文字・数字・ハイフンで40文字以内", "name":"40文字以内", "icon":"絵文字1つ", "domain":"対象と対象外、2000文字以内", "tone":"2000文字以内", "instructions":"8000文字以内", "model":"lowまたはmiddleまたはhigh", "tools":[]}。
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
