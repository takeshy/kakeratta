package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
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
必ずJSONで {"message":"完成案の短い説明", "placeholder":"追加修正の具体例", "draft":完全なskillオブジェクト, "checkIn":"人物の担当だけ。daily か monthly"} を返します。例外として、担当分野を初めて確認する場合だけdraftをnullにします。
placeholderはユーザーが次に入力する回答の短い具体例です。必ずユーザーの言語で「例：」から始め、今回の質問と会話の担当分野に合わせて毎回作り直してください。質問文そのものや「回答してください」のような抽象的な案内にしないでください。
例えばワイン担当の案への修正なら「例：飲んだワインの名前と、好きだった理由を覚えてほしい。」、担当分野の確認なら「例：読んだ本の感想を覚える担当。」のようにします。
完成案を返すときのplaceholderは、その完成案に対する追加修正の具体例にしてください。例は選択肢の制約ではなく入力のヒントです。本文と同じく専門用語を避け、120文字程度までにしてください。
skill: {"id":"英小文字・数字・ハイフンで40文字以内", "name":"40文字以内", "icon":"絵文字1つ", "domain":"短い担当条件、原則1〜2文・日本語40〜100文字程度、上限2000文字", "tone":"2000文字以内", "memory":"メモの分類。2000文字以内", "instructions":"8000文字以内", "tools":[]}。人物の担当の skill は {"id", "name", "icon", "kind":"person", "aliases":[], "keywords":[], "domain":"関係性", "instructions":"気を配ること", "tools":[]} で、tone と memory はありません。
domainは振り分け用の「担当条件」です。どんな投稿が対象かを肯定形で1〜2文、日本語なら40〜100文字程度にまとめます。短さのためにユーザーが明示した条件を落としたり、担当範囲を変えたりしないでください。
対象の話題・写真・記録を簡潔に書き、依頼や質問を前提にしません。例は必要なら1つまで。除外事項の列挙、スレッドの読み方など全担当共通の説明、返事・記憶・道具の手順は入れません。
対象が複数ある場合は「または」で分け、同時に必要な条件は「かつ」で区別します。「判定する話」「一緒に考える相談」のような行動や依頼を条件にせず、投稿にある話題・写真そのものを書きます。
例：「料理・食事の写真。または食材のプリン体に関する話。」返事・記憶・道具の使い方はinstructionsに分けます。既存案も修正する担当条件はこの形に整えます。
domain、tone、memory、instructionsはそのままユーザーに表示します。JSON、Markdown記法、index.mdなどの実装用語は本文に含めず、誰でもわかる短い自然な文章で書いてください。
memoryは「メモの分類」です。必ず書きます。覚える対象の種類（型）を2〜4つ決め、型の名前は Wine や Preference のような大文字始まりの英単語にして、それぞれ何を書くか・いつまとめるかを添えます。例：「型は Wine（銘柄。産地・品種・感想）と Preference（好みの傾向。同じ好みが3回出たら統合）の2つ。」型はこの欄に書いたものだけが使われ、同じ対象は型が違っても1つのメモです。あわせて、メモの tags に何を付けるか（例：産地・品種・色）も一言書いてください。
instructionsには引用と本人の意見を分けること、推測と明記すること、代表例と反例を残すこと、どんなときに返事するかを具体的に書いてください。何を覚えるかの分類はmemoryに書き、instructionsでは繰り返しません。記憶の形式自体はアプリが別に渡します。
特定の人物（家族・友人・同僚など、ユーザーの知り合いの一人）についての担当を頼まれたら、人物の担当にします。skill に "kind":"person"、"aliases":[呼び名]、"keywords":[キーワード] を加えます。人物の担当には担当条件による振り分けがなく、投稿に呼び名かキーワードがそのまま書かれているときだけ届きます。
aliasesはその人の呼ばれ方をすべて書きます。苗字・下の名前・フルネーム・あだ名・続柄（例：母、お袋、おかん）と、そのひらがな・カタカナ・ローマ字表記です。敬称（さん・くん等）は付けません。同姓の有名人に誤って届かないよう、一般的すぎる語だけにはしないでください。
keywordsは名前を出さなくてもその人の話だとわかる語です（例：その人の家族やペットの名前、その人の職場、その人と続けている習慣）。ユーザーが挙げた語ごとに、言い換え・表記ゆれ・近い意味の語も別々の要素として並べます（例：「釣り」なら 釣り, つり, 釣行, 海釣り, 渓流）。一般的すぎて他の話にも出る語は入れません。呼び名とキーワードは合わせて80個まで、1つ40文字以内です。
人物の担当の checkIn は、その人の様子をどれくらいの頻度で振り返るかです。家族・恋人・親しい友人・毎日のように関わる同僚など日常的に関わる人は "daily"、たまに会う友人・親戚・昔の知り合いなどは "monthly" にします。monthly の担当は月に一度、本体が書くその月のまとめを読んで振り返ります。ユーザーが頻度を言ったらそれに従います。
人物の担当は、その人本人ではなく、ユーザーとその人との間を見守る担当（コーディネーター）です。本人になりきって話す担当にはしません。話す相手はユーザーで、name はその人の呼び名（担当の名札）です。
人物の担当のdomainは、ユーザーから見たその人との関係性を短く書きます（例：「恋人。」「母。」「大学時代の友人で、今は同じ会社の同僚。」）。誕生日や好みなどの事実は書きません。
人物の担当の話し方、メモの分類、約束・貸し借り・会う予定・誕生日の扱いは、本体がすべての人物の担当に共通で渡します。instructionsは、その人に特有の扱いです（例：「母には健康の話は控えめに」「仕事の相談は結論から」）。ユーザーの依頼から読み取れることを書き、共通の扱いは書きません。人物の担当の tools には、利用可能な web_search・place_search・route・schedule をすべて入れます（贈り物や会う場所を調べ、会う日や誕生日に自分を起こすため）。
人物の担当では、ユーザーが伝えた事実（誕生日・好み・得意なこと・家族・仕事・これまでの出来事など）を draft.basicInfo に基本情報として書きます。基本情報は担当の定義に保存され、ユーザーが編集できます。タイムラインに投稿したり、メモにする必要はありません。8000文字以内で、ユーザーの言語で簡潔に整理し、推測や補足を加えず、日付や数字は伝えられたとおりに書きます。既存の基本情報を保持して今回の追加・修正を反映し、依頼されていない情報を削除しないでください。事実がなければ basicInfo を "" にします。人物のdraftにはbasicInfoを必ず含めてください。
事実は domain・instructions・memory に書き写さないでください。好みや得意なこと（例：コスメ、タイ料理、ピアノ）はその人を指す語ではないので keywords にもしません。人物以外の担当では basicInfo を付けません。
既存の人物の担当に「キーワードを足して」と頼まれたら、その語と類似語をkeywordsに加え、既存の呼び名とキーワードは残します。人物以外の担当には kind・aliases・keywords を付けません。
道具は必要なものだけ利用可能なリストから選びます。新規IDは既存IDと重複させないでください。
保存はユーザーが別の保存ボタンで行います。保存済みと発言しないでください。`

// personTools are the built-in tools a new person's persona starts with.
var personTools = []string{"web_search", "place_search", "route", "schedule"}

// maxOpening bounds the question the dialog opened with, which is the app's
// own text and never long.
const maxOpening = 500

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
	// The dialog opens with its own question - who the person is, or what the
	// persona is for - so the answer to it is read as that. The model is
	// handed the question as context and the conversation from the user's
	// first line, as it always was.
	opening := ""
	if first := body.Messages[0]; first.Role == "assistant" {
		opening = strings.TrimSpace(first.Content)
		if opening == "" || len([]rune(opening)) > maxOpening {
			fail(w, 400, "会話の形式が不正です")
			return
		}
		body.Messages = body.Messages[1:]
	}
	if len(body.Messages) == 0 {
		fail(w, 400, "回答を入力してください")
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
	skills := s.catalogue.Snapshot(r.Context())
	var original *persona.Skill
	if body.ID != "" {
		skill, ok := skills.Find(body.ID)
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
	for _, skill := range skills.All() {
		ids = append(ids, skill.ID)
	}
	client, settings := s.client()
	existing := original
	if existing == nil {
		existing = body.Current
	}
	context, _ := json.Marshal(map[string]any{"existing": existing, "usedIDs": ids, "availableTools": tools.New(s.tools).Names(), "language": body.Language})
	system := builderPrompt + "\n設定データ:\n" + string(context)
	if opening != "" {
		system += "\n\nこの会話は、画面がユーザーに最初にこう尋ねて始まりました（ユーザーの最初の発言はその答えです）:\n" + opening
	}
	messages := []llm.Message{{Role: "system", Content: system}}
	messages = append(messages, body.Messages...)
	if body.EntryID != "" {
		entry, ok := s.store.Entry(body.EntryID)
		if !ok || entry.Deleted {
			fail(w, http.StatusNotFound, "投稿が見つかりません")
			return
		}
		// Only this conversation is read, not the whole timeline.
		thread, err := store.SearchThread(r.Context(), s.store, entry.ID)
		if errors.Is(err, store.ErrSearchThreadMissing) {
			fail(w, http.StatusNotFound, "投稿が見つかりません")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Use the same rendering as the router, including media, sources and
		// conversation context. The browser's excerpt is only for display.
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
	model := settings.HighModel
	reply, err := client.Chat(r.Context(), llm.Request{Model: model, JSON: true, Temperature: 0.4, Messages: messages})
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	var result struct {
		Message     string         `json:"message"`
		Placeholder string         `json:"placeholder"`
		Draft       *persona.Skill `json:"draft"`
		// CheckIn is how often a person is looked back on: "daily" or
		// "monthly", chosen from how close they are. Saved with the persona.
		CheckIn string `json:"checkIn,omitempty"`
	}
	if err := llm.DecodeJSON(reply, &result); err != nil || strings.TrimSpace(result.Message) == "" {
		fail(w, 502, "AIの応答を読めませんでした。もう一度送信してください。")
		return
	}
	if result.Draft == nil || !result.Draft.IsPerson() {
		result.CheckIn = ""
	} else if result.CheckIn != store.CheckInMonthly {
		result.CheckIn = "daily"
	}
	if result.Draft != nil {
		if original != nil {
			result.Draft.ID = original.ID
		}
		// Whether a persona is a person is settled when it is made: the AI
		// revising one keeps the kind it was saved with, whatever it proposes.
		if body.ID != "" {
			if saved, ok := skills.Find(body.ID); ok {
				result.Draft.Kind = saved.Kind
				if !saved.IsPerson() {
					result.Draft.Aliases, result.Draft.Keywords, result.Draft.BasicInfo = nil, nil, ""
					result.CheckIn = ""
				}
			}
		}
		if existing != nil && existing.IsPerson() && result.Draft.IsPerson() {
			var fields struct {
				Draft map[string]json.RawMessage `json:"draft"`
			}
			if llm.DecodeJSON(reply, &fields) == nil {
				if _, present := fields.Draft["basicInfo"]; !present {
					result.Draft.BasicInfo = existing.BasicInfo
				}
			}
		}
		result.Draft.Builtin = false
		result.Draft.Customized = false
		result.Draft.TidyTerms()
		// A person's persona is told to be up on the morning of a meeting, and
		// only a placed wake gets it up; a meeting place, the way there and a
		// present are what it looks up for someone. A new person gets them all.
		if original == nil && result.Draft.IsPerson() {
			for _, name := range personTools {
				if s.knownTool(name) && !slices.Contains(result.Draft.Tools, name) {
					result.Draft.Tools = append(result.Draft.Tools, name)
				}
			}
		}
		if err := persona.Validate(*result.Draft, s.knownTool); err != nil {
			fail(w, 502, err.Error())
			return
		}
		if original == nil {
			if _, exists := s.catalogue.Find(r.Context(), result.Draft.ID); exists {
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
	previous, exists := s.catalogue.Find(r.Context(), skill.ID)
	if id == "" && exists {
		fail(w, 409, "同じIDの担当があります")
		return
	}
	if id != "" && (!exists || id != skill.ID) {
		fail(w, 400, "担当のIDは変更できません")
		return
	}
	if exists && previous.IsPerson() != skill.IsPerson() {
		fail(w, 400, "人物の担当と興味の担当は入れ替えられません")
		return
	}
	if err := persona.Validate(skill, s.knownTool); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var calendar *persona.PersonSchedulePlan
	if skill.IsPerson() && (!exists || previous.BasicInfo != skill.BasicInfo || !previous.IsPerson()) {
		var err error
		calendar, err = s.runner.PreparePersonSchedules(r.Context(), skill)
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
	}
	raw, _ := json.Marshal(skill)
	var saved persona.Skill
	var err error
	if id == "" {
		saved, err = s.catalogue.Install(raw, s.knownTool)
	} else {
		saved, err = s.catalogue.Update(r.Context(), id, raw, s.knownTool)
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if calendar != nil {
		if err := s.runner.ApplyPersonSchedules(calendar); err != nil {
			fail(w, 500, "担当は保存しましたが、通知予定を登録できませんでした："+err.Error())
			return
		}
	}
	if exists {
		writeJSON(w, 200, saved)
		return
	}
	writeJSON(w, 200, added{Skill: saved, Active: s.activatePersona(saved.ID) == nil})
}

// personaBuilderModel writes the conditions routing depends on, so it remains
// Ultra even though routing itself runs on Super or the optional Jev service.
func personaBuilderModel() string {
	return store.NemotronUltra
}
