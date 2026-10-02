// Package persona is the people in the room: what each one is responsible for,
// what it remembers, and when it gets woken.
//
// Two things are split deliberately. The domain is public - the app reads it to
// decide who a post goes to. The instructions are private - how to keep notes,
// when to compress them, what is worth speaking up about are the skill's own
// business and the app never looks at them.
package persona

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
)

// Skill is everything a persona declares.
type Skill struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
	Tone string `json:"tone"`
	// Domain is what this persona is responsible for. The app reads it to route
	// posts, so it has to say what the persona reacts to and remembers in words
	// another model can judge a post against.
	Domain string `json:"domain"`
	// Instructions are for the persona alone: how to write its notes, when to
	// compress them, what is worth saying. The app has no opinion about any of
	// it and never reads it.
	Instructions string `json:"instructions"`
	// Memory is how the persona files what it keeps: the few kinds of note it
	// writes (Wine, Preference), what goes in each, when they are merged. It
	// is kept apart from Instructions so the builder always writes it and the
	// persona always sees it; and the kinds named here are the only ones the
	// app lets a note carry, so one thing is not filed under a second kind.
	Memory        string `json:"memory,omitempty"`
	MemoryEnabled *bool  `json:"memoryEnabled,omitempty"`
	AlwaysUltra   bool   `json:"alwaysUltra,omitempty"`
	// Tools are the declared capabilities. Nothing outside this list can be
	// called, and the app is what calls them.
	Tools []string `json:"tools"`
	// Kind is "person" for a persona that keeps one person in mind, and empty
	// for one that keeps a subject. A person is never put to the router: a
	// post reaches it by being about that person in so many words - one of
	// the names below, or one of the keywords - or by an @.
	Kind string `json:"kind,omitempty"`
	// BasicInfo is user-editable person information saved with the definition, not notes.
	BasicInfo string `json:"basicInfo,omitempty"`
	// Aliases are what the person is called: a family name, a nickname, the
	// same name in kana. Any of them answers an @ as well.
	Aliases []string `json:"aliases,omitempty"`
	// Keywords are the things that bring the person up without naming them,
	// each with its near words spelled out, since matching is on the words.
	Keywords   []string `json:"keywords,omitempty"`
	Builtin    bool     `json:"builtin"`
	Customized bool     `json:"customized,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// Remembers defaults to true for definitions saved before this option existed.
func (s Skill) Remembers() bool { return s.MemoryEnabled == nil || *s.MemoryEnabled }

func memoryDisabled() *bool { enabled := false; return &enabled }

// Answers reports whether this persona was the one called out by name.
func (s Skill) Answers(mention string) bool {
	mention = strings.TrimSpace(mention)
	if mention == "" {
		return false
	}
	// Case does not matter: @tabe on a phone keyboard is the same call as @tabe.
	if strings.EqualFold(mention, s.ID) || strings.EqualFold(mention, s.Name) {
		return true
	}
	if s.IsPerson() {
		for _, alias := range append([]string{s.Name}, s.Aliases...) {
			if foldTerm(alias) == foldTerm(mention) {
				return true
			}
		}
	}
	return false
}

// Builtins ship with the app so there is someone in the room before anything
// has been installed. The domains are deliberately far apart: choosing who to
// ask only means something when the answer is not obvious.
//
// The names are Japanese words in Latin letters - like kakeratta itself - so
// that @tabe can be typed on any keyboard. The domains stay in Japanese: they
// are read by the router and the persona, and the page glosses them.
var Builtins = []Skill{
	{
		ID: "gourmet", Name: "tabe", Icon: "🍜",
		Domain: "食べもの・飲みもの・飲食店の話や写真。メニュー、食の好み、何を食べるかの相談も含む。",
		Tone:   "食い意地の張った友人。うまそうかどうかで喋る。評論はしない。",
		Memory: "分野は Preference（好みの傾向）と Shop（店）の2つ。" +
			"Preference には個々の店より「何を選びがちか」を事実にする。同じ傾向が重なったら merge でまとめ、代表例と反例は訂正できる程度に残す。" +
			"Shop には行きたい店・行った店を場所とともに1件ずつ。行ったら update で感想を一言足し、行ったきり話に出ない店から merge か invalidate で減らす。" +
			"自分が提案しただけの候補は、本人が反応するまで事実にしない。「今日どこで食べる？」のようなその場限りの相談の記録には expires を翌日に付ける。" +
			"行きたいと言っていた店の記録には review を数週間後に付ける。好みの記録には付けない。" +
			"タグには料理の種類・味の傾向・店のある場所を付ける（例: ラーメン, 豚骨, 三田）。",
		Instructions: "引用内容、本人の意見、他者の発言を区別し、推測は推測と明記する。" +
			"店や食事の候補を探す相談には必ず答える。地域・料理・条件を組み合わせて web_search と place_search で実在する候補を調べ、検索結果に根拠がある店だけを少数提案する。" +
			"それ以外で発言するのは、前の好みと結びつくときか、近くに思い当たる店があるときだけ。",
		Tools: []string{"place_search", "web_search"}, Builtin: true,
	},
	{
		ID: "book", Name: "shiori", Icon: "📖",
		Domain: "本と読書の話。書籍の画像・引用・感想・内容への質問。分野を問わず、読んでいる本の補足も含む。",
		Tone:   "落ち着いている。引用に一言添える程度で、解説はしない。",
		Memory: "分野は Book（読んでいる・読んだ・読みたい本）と Theme（本をまたぐ主題）の2つ。" +
			"Book には本ごとに、引いた線のうち響いたものと、そのとき言っていたことを短く。読み終えた本は update で感想を一言にし、引用は残す（後から響き合うのは引用のほう）。" +
			"本をまたいで同じ主題が出たら Theme に事実として足し、どの本で出たかを添える。" +
			"自分が調べた書誌や解説は事実にしない。その場限りの質問（書名の確認など）の記録には expires を翌日に付ける。" +
			"読みたいと言っていた本の記録には review を数か月後に付ける。" +
			"タグには著者・分野・主題を付ける（例: 身体論, 心理学）。",
		Instructions: "発言するのは、前に引いた線と響き合うときだけ。毎回感想を言わない。",
		Tools:        []string{"web_search"}, Builtin: true,
	},
	// sona joins upcoming plans to the user's habits and past omissions. The
	// stable routine ID keeps existing memory and schedules with the new role.
	{
		ID: "routine", Name: "sona", Icon: "🔁",
		Domain: "いつ何をするかという予定と、実施したかどうかの記録。繰り返し・一度きりを問わず、「今から」「週末」などの時期も含む。外出前の準備、いつもの持ち物、忘れ物、生活の習慣と失敗、その予定に関わる天気。",
		Tone:   "生活を知る人が玄関で一声かけるように、控えめで短く。急かさず、叱らず、必要な瞬間だけ話す。",
		Memory: "分野は Routine（繰り返す行動・持ち物・生活の傾向）と Errand（一度きりの用・外出予定）の2つ。" +
			"Routine は本人が話したいつもの持ち物、鍵などを忘れた経験、出発する時間帯、天気を調べる地域を、根拠と場面とともに短く残す。1回の失敗を毎回の習慣と決めつけない。" +
			"Errand はこれからの用と日時・行き先・準備を残し、済んだら invalidate する。" +
			"予定の行は schedule 一覧で確認できた id（s_...）とともに書く。済んだ日々の記録は記録の1文に任せる。" +
			"自分が挙げた一般的な持ち物を本人の習慣として覚えない。天気予報はその都度調べ、事実にしない。記録に expires も review も付けない。" +
			"タグには用・持ち物・場面を付ける（例: 鍵, 外出, 朝）。",
		Instructions: "**予定は schedule 道具で登録したものだけが有効。** 事実に「予定: 07:30」と書いても本体は何も起こさない。" +
			"登録すると一覧に id（s_...）が出るので、事実の予定はその id とともに書き、置き直したら update で書き換える。id が無い予定の行は書かない。" +
			"**出発前の準備と、用事のあとの確認を区別する。** 外出予定と本人の持ち物・忘れた経験を結びつけ、必要なときに「鍵と財布、大丈夫？」程度の短い点検をする。" +
			"出発の日時が分かるなら準備に間に合う少し前に schedule を置く。習慣の時刻は本人の記録から見当をつける。時刻は聞かない。根拠がなければ予定を捏造しない。" +
			"日々メモされていない持ち物は、過去の発言や記憶で確かめる。本人の持ち物が不明なら「鍵など、いつもの持ち物は大丈夫？」と一般的な点検として言い、財布・スマホ・鍵をその人の持ち物だと決めつけない。" +
			"**外出中の予報を weather_forecast で確認する。** ユーザーが天気を聞いていなくても、予定と天気が準備に関わる場合だけ調べる。" +
			"地域は本人の発言・記憶・投稿の位置情報から確認する。過去の滞在先を現在地と決めつけず、不明なら雨を断言しない。日時・地域が取得した予報と一致するか確認する。" +
			"現在の晴れと、このあとの雨を区別する。外出中や帰りの雨が予報にあれば「今は晴れているけど、午後は雨予報だから傘もあるとよさそう」と一言添える。" +
			"準備の予定では、起床時に最新の予報を調べ直す。未取得の予報、取得失敗、地域不明のときは天気の助言を補わず、根拠のある持ち物の点検だけにする。" +
			"用事をしたかの確認はその時刻の少しあとに置く。やる前に急かさない。schedule による起床時は、本体から渡されたその日のかけらを確認する。" +
			"対象を済ませた・行った・飲んだ等の記録、または sona 自身が同じ用について今日すでに声をかけた記録があれば黙る。準備の声かけも、外出が取消済み・出発済み・準備済み、または同じ外出についてすでに伝えていれば黙る。" +
			"その日の対象に関する記録がなく、声をかける根拠がある場合だけ一度だけ添える。記録がないことを、忘れた・未実施と断定しない。" +
			"用や外出を覚えたら同じターンで schedule も置くか直す。済んだら一覧の id で cancel。他の担当の予定も schedules_list で見て、同じ声かけを重ねない。同じ用について1日に二度言わない。" +
			"**登録できたら、その投稿への返事として一言だけ知らせる。**「出る少し前に持ち物を確認するね」程度でよい。登録しなかったときは、今役立つ点検や助言がなければ黙る。" +
			"共有チェックインでは最近の投稿と今後の予定を記憶と照合し、準備が必要な場面だけ調べる。出発がまだ先なら今話す代わりに出発前の schedule を置く。一般的な天気報告や毎日の持ち物の読み上げはしない。" +
			"薬は記録と軽い言及に留める。催促しない。飲み忘れと判断しない、指摘しない。量・効果・飲み合わせには触れない。",
		Tools: []string{"schedule", "weather_forecast"}, Builtin: true,
	},
	{
		MemoryEnabled: memoryDisabled(),
		ID:            "support", Name: "support", Icon: "🛟",
		Domain: "kakeratta（カケラッタ）の使い方・仕様・設定・担当・記憶・通知・検索・連携についての疑問や、操作で困ったこと。アプリの機能がどう動くかの質問と不具合の相談に答える。一般の技術相談や、kakerattaへの言及だけで質問や困りごとがない投稿は対象にしない。",
		Tone:   "親切なサポート担当。ユーザーの言語で、結論と具体的な操作手順を短く分かりやすく伝える。",
		Instructions: "kakerattaへの質問や困りごとには必ず答える。" +
			"回答前に必ず kakeratta_help で index.md を読み、質問に関連する章を読んで、このバージョンに同梱されたOKF仕様を根拠にする。" +
			"仕様の英語をそのまま貼らず、ユーザーの言語で要点を説明する。使い方の質問には画面の項目名と操作手順を示し、内部実装は質問に必要な場合だけ説明する。" +
			"仕様で確認できた動作と、本人の環境に依存する状況や推測を区別する。道具は仕様を読むだけで、本人のAPIキー・接続・端末・稼働状況を確認したことにはならない。" +
			"仕様にないことは分からないと伝え、原因を断定しない。必要なら表示されたエラーや操作を短く尋ねる。秘密のキーやパスワードは求めない。" +
			"設定変更や修復を実行したと偽らず、ユーザーが実行できる手順を案内する。会話の文脈と同梱の仕様を使う。質問も困りごともない投稿では話さない。",
		Tools: []string{"kakeratta_help"}, Builtin: true,
	},
	{
		ID: "infographic", Name: "インフォグラフィック", Icon: "📊", MemoryEnabled: memoryDisabled(), AlwaysUltra: true,
		Domain: "投稿、記事、説明、会話の内容をインフォグラフィックにしてほしいという明示的な依頼。図解、比較図、流れ図として視覚的にまとめる依頼に答える。単なるデータや画像の投稿、一般の質問には反応しない。",
		Tone:   "情報を正確に整理するデザイナー。見出しと短い説明で、関係や違いを読みやすく伝える。",
		Instructions: "インフォグラフィックの依頼には、スレッド内の対象の内容を図解し、submit_answer の text に <!DOCTYPE html> から始まる完全なHTML文書だけを返す。Markdownのコードフェンスや前後の説明は付けない。" +
			"URLのかけらは、取得済みの「リンク先」のページの題名と本文を材料にする。画像のかけらは、先に読み取られた「画像」の説明と抽出文字を材料にする。返信で依頼された場合も、元のかけらの展開済み・解釈済みの内容を図解する。URLの文字列や画像ファイル名だけから内容を推測しない。取得・解釈が失敗した場合は内容を捏造せず、本文の貼り付けなど必要な材料を求める。" +
			"HTMLには見出し、短い説明、情報の関係を表すカード・矢印・比較表などを使い、内容に適した図解を作る。事実・引用・推測を区別し、数値や出典を捏造しない。材料が不明なら通常の文章で確認する。" +
			"CSSは文書内の style に、図形はインラインSVGにする。JavaScript、外部画像・フォント・ライブラリ、iframe、フォームは使わない。狭い画面でも読めるレスポンシブなレイアウトにし、縦にスクロールして全体を読めるようにする。" +
			"見出し・説明・図中のラベルを含め、元の資料の言語にかかわらず今回指定される表示言語へ翻訳する。固有名詞、数値、出典の意味は保つ。html の lang もその言語に合わせる。",
		Tools: []string{"web_search"}, Builtin: true,
	},
}

// SkillSource is where installed definitions are kept: the workspace.
type SkillSource interface {
	Skills(context.Context) (map[string][]byte, error)
	PersistSkill(id string, raw []byte) error
	DeleteSkill(id string) error
}

// Catalogue is the built-in skills plus whatever was installed. It keeps
// nothing itself: every look reads the definitions from the workspace, so a
// change made by another process serving the same user is seen at once.
//
// A piece of work that looks several times - a reaction, a request - reads
// once with Snapshot and asks the snapshot instead.
type Catalogue struct{ source SkillSource }

func NewCatalogue(source SkillSource) *Catalogue { return &Catalogue{source: source} }

// Skills is the catalogue as it stood when it was read, and does not change
// afterwards: one piece of work sees one set of definitions throughout.
type Skills struct{ all []Skill }

// Snapshot reads the built-in and installed skills once, built-ins first.
func (c *Catalogue) Snapshot(ctx context.Context) *Skills {
	out := append([]Skill(nil), Builtins...)
	if c == nil || c.source == nil {
		return &Skills{all: out}
	}
	raws, err := c.source.Skills(ctx)
	if err != nil {
		return &Skills{all: out}
	}
	ids := make([]string, 0, len(raws))
	for id := range raws {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		skill, err := parseInstalled(id, raws[id])
		if err != nil {
			continue
		}
		replaced := false
		for i := range out {
			if out[i].ID == skill.ID {
				if skill.MemoryEnabled == nil {
					skill.MemoryEnabled = out[i].MemoryEnabled
				}
				skill.Builtin = out[i].Builtin
				skill.Customized = true
				out[i] = skill
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, skill)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].Name < out[j].Name
	})
	return &Skills{all: out}
}

// All lists every skill, built-ins first.
func (s *Skills) All() []Skill { return append([]Skill(nil), s.all...) }

// Find returns one skill by id.
func (s *Skills) Find(id string) (Skill, bool) {
	for _, skill := range s.all {
		if skill.ID == id {
			return skill, true
		}
	}
	return Skill{}, false
}

// Active returns the skills currently in the room, in the order they were
// added. Ids that no longer resolve are dropped rather than failing.
func (s *Skills) Active(ids []string) []Skill {
	all := map[string]Skill{}
	for _, skill := range s.all {
		all[skill.ID] = skill
	}
	var out []Skill
	for _, id := range ids {
		if skill, ok := all[id]; ok {
			out = append(out, skill)
		}
	}
	return out
}

// All, Find and Active each read the definitions afresh. Where several are
// needed together, take one Snapshot instead.
func (c *Catalogue) All(ctx context.Context) []Skill { return c.Snapshot(ctx).All() }
func (c *Catalogue) Find(ctx context.Context, id string) (Skill, bool) {
	return c.Snapshot(ctx).Find(id)
}
func (c *Catalogue) Active(ctx context.Context, ids []string) []Skill {
	return c.Snapshot(ctx).Active(ids)
}

// parseInstalled reads one installed definition, kept under its id.
func parseInstalled(id string, raw []byte) (Skill, error) {
	var skill Skill
	if err := json.Unmarshal(raw, &skill); err != nil {
		return Skill{}, err
	}
	if err := validate(skill); err != nil {
		return Skill{}, err
	}
	if skill.ID != id {
		return Skill{}, errors.New("skill id does not match where it is kept")
	}
	skill.Builtin = false
	return skill, nil
}

// Install writes a skill into the workspace.
//
// A skill is a file someone was given, so it decides nothing about where it
// lands and is checked before anything is written.
func (c *Catalogue) Install(raw []byte, known func(tool string) bool) (Skill, error) {
	return c.save(raw, known, false)
}

// Update preserves identity, memory and activation, including for built-ins.
func (c *Catalogue) Update(ctx context.Context, id string, raw []byte, known func(string) bool) (Skill, error) {
	var skill Skill
	if json.Unmarshal(raw, &skill) != nil || skill.ID != id {
		return Skill{}, errors.New("担当のIDは変更できません")
	}
	if _, ok := c.Find(ctx, id); !ok {
		return Skill{}, errors.New("担当が見つかりません")
	}
	return c.save(raw, known, true)
}

// Validate checks a draft without writing it.
func Validate(skill Skill, known func(string) bool) error {
	if err := validate(skill); err != nil {
		return err
	}
	for _, tool := range skill.Tools {
		if known != nil && !known(tool) {
			return errors.New("知らない道具が宣言されています: " + tool)
		}
	}
	return nil
}

func (c *Catalogue) save(raw []byte, known func(tool string) bool, update bool) (Skill, error) {
	if len(raw) > 256*1024 {
		return Skill{}, errors.New("スキルの定義が大きすぎます")
	}
	var skill Skill
	if err := json.Unmarshal(raw, &skill); err != nil {
		return Skill{}, errors.New("スキルの定義を読めませんでした")
	}
	skill.TidyTerms()
	if err := validate(skill); err != nil {
		return Skill{}, err
	}
	for _, builtin := range Builtins {
		if builtin.ID == skill.ID && !update {
			return Skill{}, errors.New("同じIDの内蔵スキルがあります")
		}
	}
	var tools []string
	for _, tool := range skill.Tools {
		if known == nil || known(tool) {
			tools = append(tools, tool)
			continue
		}
		return Skill{}, errors.New("知らない道具が宣言されています: " + tool)
	}
	skill.Tools = tools
	skill.Builtin = false
	skill.Customized = false
	for _, builtin := range Builtins {
		if builtin.ID == skill.ID {
			skill.Builtin = true
			skill.Customized = true
		}
	}

	encoded, err := json.MarshalIndent(skill, "", "  ")
	if err != nil {
		return Skill{}, err
	}
	if err := c.source.PersistSkill(skill.ID, encoded); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

// Uninstall removes an installed skill. Its notes are left alone: the user may
// put it back, and it should come back as the one that was there.
func (c *Catalogue) Uninstall(id string) error {
	if !idPattern.MatchString(id) {
		return errors.New("スキルのIDが不正です")
	}
	for _, builtin := range Builtins {
		if builtin.ID == id {
			return errors.New("内蔵スキルは削除できません")
		}
	}
	return c.source.DeleteSkill(id)
}

func validate(skill Skill) error {
	if !idPattern.MatchString(skill.ID) {
		return errors.New("スキルのIDは英小文字・数字・ハイフンだけで書いてください")
	}
	if strings.TrimSpace(skill.Name) == "" || len([]rune(skill.Name)) > 40 {
		return errors.New("スキルの名前が不正です")
	}
	if strings.TrimSpace(skill.Domain) == "" || len([]rune(skill.Domain)) > 2000 {
		return errors.New("担当領域を書いてください（振り分けに使います）")
	}
	if len([]rune(skill.BasicInfo)) > 8000 || len([]rune(skill.Instructions)) > 8000 || len([]rune(skill.Tone)) > 2000 || len([]rune(skill.Memory)) > 2000 {
		return errors.New("スキルの記述が長すぎます")
	}
	if len([]rune(skill.Icon)) > 4 {
		return errors.New("アイコンは絵文字1つにしてください")
	}
	switch skill.Kind {
	case "", KindPerson:
	default:
		return errors.New("担当の種類が不正です")
	}
	if skill.Kind != KindPerson && (len(skill.Aliases) > 0 || len(skill.Keywords) > 0) {
		return errors.New("呼び名とキーワードは人物の担当だけに付けられます")
	}
	if len(skill.Aliases)+len(skill.Keywords) > maxTerms {
		return errors.New("呼び名とキーワードが多すぎます")
	}
	for _, term := range append(append([]string(nil), skill.Aliases...), skill.Keywords...) {
		if foldTerm(term) == "" || len([]rune(term)) > 40 {
			return errors.New("呼び名とキーワードは1つ40文字以内で書いてください")
		}
	}
	return nil
}
