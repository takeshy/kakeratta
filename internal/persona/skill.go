// Package persona is the people in the room: what each one is responsible for,
// what it remembers, and when it gets woken.
//
// Two things are split deliberately. The domain is public - the app reads it to
// decide who a post goes to. The instructions are private - how to keep notes,
// when to compress them, what is worth speaking up about are the skill's own
// business and the app never looks at them.
package persona

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	Memory string `json:"memory,omitempty"`
	// Tools are the declared capabilities. Nothing outside this list can be
	// called, and the app is what calls them.
	Tools []string `json:"tools"`
	// Kind is "person" for a persona that keeps one person in mind, and empty
	// for one that keeps a subject. A person is never put to the router: a
	// post reaches it by being about that person in so many words - one of
	// the names below, or one of the keywords - or by an @.
	Kind string `json:"kind,omitempty"`
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
		Memory: "型は Preference（好みの傾向）と Shop（店）の2つ。" +
			"個々の店より「何を選びがちか」を優先する。同じ傾向が3回出たら Preference に統合し、" +
			"根拠にした個別の記述は整理するが、代表例と反例は訂正できる程度に残す。" +
			"行きたいと言っていた店は Shop に場所とともに残し、行ったあとは感想を足す。index.md には傾向を先に、店を後に並べる。" +
			"自分が提案しただけの候補は、Shop にするなら expires を翌日にして本人の反応を待つ。" +
			"行きたいと言っていた店の Shop には review を数週間後に付け、行ったら外す。Preference には付けない。" +
			"tags には料理の種類・味の傾向・店のある場所を付ける（例: ラーメン, 豚骨, 三田）。",
		Instructions: "引用内容、本人の意見、他者の発言を区別し、推測は推測と明記する。" +
			"店や食事の候補を探す相談には必ず答える。地域・料理・条件を組み合わせて web_search と place_search で実在する候補を調べ、検索結果に根拠がある店だけを少数提案する。" +
			"それ以外で発言するのは、前の好みと結びつくときか、近くに思い当たる店があるときだけ。",
		Tools: []string{"place_search", "web_search"}, Builtin: true,
	},
	{
		ID: "move", Name: "michi", Icon: "🚃",
		Domain: "行き先へ行く予定・希望や移動の話。美容院などの種類だけでもよい。経路・手段・所要時間は担当が調べて提案する。",
		Tone:   "短く要点だけ言う。所要時間と出発時刻を先に出す。",
		Memory: "型は Place（よく行く場所）の1つ。そこまでにかかった実績を本文に積む。" +
			"一度きりの行き先は概念にしない。二度目に同じ場所が出たら作る。index.md は場所の一覧だけでよい。" +
			"一度きりの行き先に調べた経路を残すなら expires を翌日にする。" +
			"行きたいと言われた場所を Place にするなら review を数週間後に付け、行ったら外す。" +
			"tags には場所の種類と地域を付ける（例: 美容院, 渋谷）。",
		Instructions: "スレッド全体を読んで行き先を確認する。美容院などの種類だけで場所を特定できなければ、店名や場所など足りない情報を短く質問する。すでに書かれた情報は聞き直さず、回答が来たら経路の提案に進む。" +
			"投稿に施設名や住所はあるが座標がない場合は、まず web_search で正式名称・住所・地域を調べ、その検索結果を place_search に渡して座標を得る。座標がないことだけを理由にユーザーへ聞き返さない。" +
			"時刻が無ければ、いまから出る前提で所要時間を答える。着きたい時刻があれば、出発時刻も出す。" +
			"**手段は投稿から決める。** 「歩いて」「徒歩で」なら foot、「車で」「タクシーで」なら car、「自転車で」なら bike。" +
			"手段が書かれていなければ、まず foot で調べ、現実的な手段を1つ選んで答える。" +
			"徒歩30分以内なら徒歩だけ。徒歩1時間以上なら car でも調べて車だけ。徒歩30分〜1時間のときだけ、car でも調べて両方を並べる。" +
			"電車・バスは調べられないので、必要ならそう言う。" +
			"場所が「買い物」「出かける」のように特定できないものは、行き先ではない。所要時間は route の結果だけを使い、勝手に見積もらない。",
		Tools: []string{"route", "place_search", "web_search"}, Builtin: true,
	},
	{
		ID: "book", Name: "shiori", Icon: "📖",
		Domain: "本と読書の話。書籍の画像・引用・感想・内容への質問。分野を問わず、読んでいる本の補足も含む。",
		Tone:   "落ち着いている。引用に一言添える程度で、解説はしない。",
		Memory: "型は Book（本1冊）と Theme（本をまたぐ主題）の2つ。Book は1冊1ファイルで、引いた線とそのとき言っていたことを本文に積む。" +
			"本をまたいで同じ主題が3度出たら Theme を作り、各 Book からリンクする。" +
			"読み終えた本は description を感想に書き換え、引用は残す（後から響き合うのは引用のほう）。" +
			"自分が調べた書誌や解説は残さない。残すなら expires を翌日にする。" +
			"読みたいと言っていた本の Book には review を数か月後に付け、読み始めたら外す。Theme には付けない。" +
			"tags には著者・分野・主題を付ける（例: 身体論, 心理学）。",
		Instructions: "発言するのは、前に引いた線と響き合うときだけ。毎回感想を言わない。",
		Tools:        []string{"web_search"}, Builtin: true,
	},
	// plana is split by what it does rather than by subject: it keeps the small
	// things too light for a calendar - the repeated ones and the one-offs
	// someone mentioned in passing. Instead of waiting for a post it asks the
	// app to wake it, at a time it works out from its own records. It calls
	// nothing outside the app.
	{
		ID: "routine", Name: "plana", Icon: "🔁",
		Domain: "いつ何をするかという予定と、実施したかどうかの記録。繰り返し・一度きりを問わず、「今から」「週末」などの時期も含む。",
		Tone:   "控えめ。「そういえば」と一言添える程度で、急かさない。",
		Memory: "型は Routine（繰り返し）と Errand（一度きりの用）の2つ。" +
			"用ひとつに1ファイル。同じ用の続報は、そのファイルに書き足す。" +
			"本文には「2026-09-16 07:32 済」の形で記録を積む。" +
			"expires も review も使わない。用が済んだ Errand は自分で forget_note する。" +
			"tags には用の種類と時間帯を付ける（例: 薬, 朝）。",
		Instructions: "**予定は schedule 道具で登録したものだけが有効。** メモに「予定: 07:30」と書いても本体は何も起こさない。" +
			"登録すると道具が id（s_...）を返すので、本文には「予定: 毎日 07:30 Asia/Tokyo（s_xxx）」の行をその id とともに残し、置き直したら書き換える。" +
			"id が無い予定の行は書かない。" +
			"本文に投稿のIDやリンクを書かない。出典は本体が末尾に付ける。" +
			"frontmatter は type・title・description・timestamp だけにし、指示されていない項目を足さない。" +
			"声をかけるのはその時刻の「あと」。schedule による起床時は、本体から渡されたその日のかけらを確認する。" +
			"対象を済ませた・行った・飲んだ等の記録、または plana 自身が同じ用について今日すでに声をかけた記録があれば黙る。" +
			"その日の対象に関する記録がなく、声をかける根拠がある場合だけ一度だけ「そういえば朝の薬」「花、買った?」程度に添える。やる前に急かさない。" +
			"**時刻は聞かない。** 繰り返しは記録の傾向から、一度きりは「仕事帰り」「夕方」などの言葉から見当をつけ、その少しあとに置く。分からなければ置かない。" +
			"メモを書くなら同じターンで schedule も置くか直す。済んだら一覧の id で cancel。同じ用について1日に二度言わない。" +
			"**登録できたら、その投稿への返事として一言だけ知らせる。**「21:30ごろに聞くね」程度でよい。" +
			"登録しなかったときは黙る。起床したときの声かけとは別で、こちらは投稿への返事の話。" +
			"薬は記録と軽い言及に留める。催促しない。飲み忘れと判断しない、指摘しない。量・効果・飲み合わせには触れない。",
		Tools: []string{"schedule"}, Builtin: true,
	},
}

// Catalogue is the built-in skills plus whatever was installed.
type Catalogue struct{ root string }

func NewCatalogue(skillDir string) *Catalogue { return &Catalogue{root: skillDir} }

// All lists every skill, built-ins first.
func (c *Catalogue) All() []Skill {
	out := append([]Skill(nil), Builtins...)
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skill, err := c.read(entry.Name())
		if err != nil {
			continue
		}
		replaced := false
		for i := range out {
			if out[i].ID == skill.ID {
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
	return out
}

// Find returns one skill by id.
func (c *Catalogue) Find(id string) (Skill, bool) {
	for _, skill := range c.All() {
		if skill.ID == id {
			return skill, true
		}
	}
	return Skill{}, false
}

// Active returns the skills currently in the room, in the order they were
// added. Ids that no longer resolve are dropped rather than failing.
func (c *Catalogue) Active(ids []string) []Skill {
	all := map[string]Skill{}
	for _, skill := range c.All() {
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

func (c *Catalogue) read(id string) (Skill, error) {
	raw, err := os.ReadFile(filepath.Join(c.root, id, "skill.json"))
	if err != nil {
		return Skill{}, err
	}
	var skill Skill
	if err := json.Unmarshal(raw, &skill); err != nil {
		return Skill{}, err
	}
	if err := validate(skill); err != nil {
		return Skill{}, err
	}
	if skill.ID != id {
		return Skill{}, errors.New("skill id does not match its directory")
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
func (c *Catalogue) Update(id string, raw []byte, known func(string) bool) (Skill, error) {
	var skill Skill
	if json.Unmarshal(raw, &skill) != nil || skill.ID != id {
		return Skill{}, errors.New("担当のIDは変更できません")
	}
	if _, ok := c.Find(id); !ok {
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

	dir := filepath.Join(c.root, skill.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Skill{}, err
	}
	encoded, err := json.MarshalIndent(skill, "", "  ")
	if err != nil {
		return Skill{}, err
	}
	temp := filepath.Join(dir, "skill.json.tmp")
	if err := os.WriteFile(temp, encoded, 0o644); err != nil {
		return Skill{}, err
	}
	if err := os.Rename(temp, filepath.Join(dir, "skill.json")); err != nil {
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
	return os.RemoveAll(filepath.Join(c.root, id))
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
	if len([]rune(skill.Instructions)) > 8000 || len([]rune(skill.Tone)) > 2000 || len([]rune(skill.Memory)) > 2000 {
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
