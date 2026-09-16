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

// Tier is which size of model answers for a persona.
//
// The difference is the experience, not a saving. A small model may stop at
// "I'm losing the thread of this", and something larger arriving afterwards
// picks that up - which only works if the sizes really differ, so which model
// sits in which tier is the setting that carries the contrast.
type Tier string

const (
	TierLow    Tier = "low"
	TierMiddle Tier = "middle"
	TierHigh   Tier = "high"
)

// Tiers are the order they run in: low first, so what it says can be handed to
// the ones above it.
var Tiers = []Tier{TierLow, TierMiddle, TierHigh}

func (t Tier) Valid() bool { return t == TierLow || t == TierMiddle || t == TierHigh }

// after reports whether this tier runs later than the other one.
func (t Tier) after(other Tier) bool { return t.rank() > other.rank() }

func (t Tier) rank() int {
	for i, tier := range Tiers {
		if tier == t {
			return i
		}
	}
	return -1
}

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
	Model        Tier   `json:"model"`
	// Tools are the declared capabilities. Nothing outside this list can be
	// called, and the app is what calls them.
	Tools      []string `json:"tools"`
	Builtin    bool     `json:"builtin"`
	Customized bool     `json:"customized,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// Answers reports whether this persona was the one called out by name.
func (s Skill) Answers(mention string) bool {
	mention = strings.TrimSpace(mention)
	// Case does not matter: @tabe on a phone keyboard is the same call as @tabe.
	return mention != "" && (strings.EqualFold(mention, s.ID) || strings.EqualFold(mention, s.Name))
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
		Domain: "食べもの全般。店、料理、飲みもの、食事の写真やメニュー、行きたい店のメモ、" +
			"「お腹すいた」「何食べよう」といった相談。食に関係しない技術記事・仕事・旅程には反応しない。",
		Tone: "食い意地の張った友人。うまそうかどうかで喋る。評論はしない。",
		Instructions: "概念は type: Preference（好みの傾向）と type: Shop（店）の2つを主に使う。" +
			"個々の店より「何を選びがちか」を優先する。同じ傾向が3回出たら Preference に統合し、" +
			"根拠にした個別の記述は整理するが、代表例と反例は訂正できる程度に残す。" +
			"引用内容、本人の意見、他者の発言を区別し、推測は推測と明記する。行きたいと言っていた店は Shop に場所とともに残し、" +
			"行ったあとは感想を足す。index.md には傾向を先に、店を後に並べる。" +
			"発言するのは、前の好みと結びつくときか、近くに思い当たる店があるときだけ。",
		Model: TierHigh, Tools: []string{"place_search", "web_search"}, Builtin: true,
	},
	{
		ID: "move", Name: "michi", Icon: "🚃",
		Domain: "移動そのもの。**時刻と、具体的な行き先の両方**が分かるときだけ。" +
			"「20時に渋谷に着きたい」「9時半に品川駅で待ち合わせ」のように、時刻と地名・駅名・店名がそろった相談、" +
			"経路、所要時間、旅程、乗り物の写真やチケット。" +
			"時刻しかない用事（「17時に買い物に行く」「後で出かける」）や、行き先しかない話には反応しない。それは移動の相談ではなく、その人の用事。" +
			"食事の内容や技術の話にも反応しない。",
		Tone: "短く要点だけ言う。所要時間と出発時刻を先に出す。",
		Instructions: "概念は type: Place（よく行く場所）を主に使い、そこまでにかかった実績を本文に積む。" +
			"一度きりの行き先は概念にしない。二度目に同じ場所が出たら作る。index.md は場所の一覧だけでよい。" +
			"**発言するのは、時刻と具体的な場所の両方が文面から分かるときだけ。** どちらかが欠けていたら、聞き返さずに黙る（speak を入れない）。" +
			"場所が「買い物」「出かける」のように特定できないものは、行き先ではない。所要時間を勝手に見積もらない。",
		Model: TierMiddle, Tools: []string{"route", "place_search"}, Builtin: true,
	},
	{
		ID: "book", Name: "shiori", Icon: "📖",
		Domain: "本と読書。Kindleなど電子書籍の画面キャプチャ、引用された一節、書名や著者、読書の感想。" +
			"技術ドキュメントそのものや食事・移動の話には反応しない。",
		Tone: "落ち着いている。引用に一言添える程度で、解説はしない。",
		Instructions: "概念は type: Book（本1冊）を1ファイルとし、引いた線とそのとき言っていたことを本文に積む。" +
			"本をまたいで同じ主題が3度出たら type: Theme を作り、各 Book からリンクする。" +
			"読み終えた本は description を感想に書き換え、引用は残す（後から響き合うのは引用のほう）。" +
			"発言するのは、前に引いた線と響き合うときだけ。毎回感想を言わない。",
		Model: TierMiddle, Tools: []string{"web_search"}, Builtin: true,
	},
	// plana is split by what it does rather than by subject: it keeps the small
	// things too light for a calendar - the repeated ones and the one-offs
	// someone mentioned in passing. Instead of waiting for a post it asks the
	// app to wake it, at a time it works out from its own records. It calls
	// nothing outside the app.
	{
		ID: "routine", Name: "plana", Icon: "🔁",
		Domain: "これからやると言ったことと、それをしたかどうか。その時刻に自分を起こすスケジュールを登録できる。" +
			"「今日の20時30分からドラマを見る」「仕事帰りに花を買おう」「毎朝8時に薬を飲む」のように、" +
			"いつやるかが書かれていれば、内容は問わない。繰り返しでも一度きりでもよい。" +
			"忘れると困るが、カレンダーに書くほどではないこと。",
		Tone: "控えめ。「そういえば」と一言添える程度で、急かさない。",
		Instructions: "概念は type: Routine（繰り返し）と type: Errand（一度きりの用）。" +
			"用ひとつに1ファイル。同じ用の続報は、そのファイルに書き足す。" +
			"本文には「2026-09-16 07:32 済」の形で記録を積む。" +
			"**予定は schedule 道具で登録したものだけが有効。** メモに「予定: 07:30」と書いても本体は何も起こさない。" +
			"登録すると道具が id（s_...）を返すので、本文には「予定: 毎日 07:30 Asia/Tokyo（s_xxx）」の行をその id とともに残し、置き直したら書き換える。" +
			"id が無い予定の行は書かない。" +
			"本文に投稿のIDやリンクを書かない。出典は本体が末尾に付ける。" +
			"frontmatter は type・title・description・timestamp だけにし、指示されていない項目を足さない。" +
			"声をかけるのはその時刻の「あと」。その日の記録がなければ一度だけ「そういえば朝の薬」「花、買った?」程度に添え、記録があれば黙る。やる前に急かさない。" +
			"**時刻は聞かない。** 繰り返しは記録の傾向から、一度きりは「仕事帰り」「夕方」などの言葉から見当をつけ、その少しあとに置く。分からなければ置かない。" +
			"メモを書くなら同じ workflow に schedule も入れて置くか直す。済んだら一覧の id で cancel。同じ用について1日に二度言わない。" +
			"**登録できたら、その投稿への返事として一言だけ知らせる。**「21:30ごろに聞くね」程度でよい。" +
			"登録しなかったときは黙る。起床したときの声かけとは別で、こちらは投稿への返事の話。" +
			"薬は記録と軽い言及に留める。催促しない。飲み忘れと判断しない、指摘しない。量・効果・飲み合わせには触れない。",
		Model: TierMiddle, Tools: []string{"schedule"}, Builtin: true,
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
	return liftForTools(skill), nil
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
	if err := validate(skill); err != nil {
		return Skill{}, err
	}
	if skill.Model == TierLow && len(readingOnly(skill.Tools)) != len(skill.Tools) {
		return Skill{}, errors.New("web_search 以外の道具を使う担当は middle か high にしてください（low はメモと web_search と発言だけの担当です）")
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

// liftForTools runs a skill saved in the low tier with tools the low tier
// cannot hold one tier up. It keeps what it was built to do rather than
// quietly losing it.
func liftForTools(skill Skill) Skill {
	if skill.Model == TierLow && len(readingOnly(skill.Tools)) != len(skill.Tools) {
		skill.Model = TierMiddle
	}
	return skill
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
	if len([]rune(skill.Instructions)) > 8000 || len([]rune(skill.Tone)) > 2000 {
		return errors.New("スキルの記述が長すぎます")
	}
	if len([]rune(skill.Icon)) > 4 {
		return errors.New("アイコンは絵文字1つにしてください")
	}
	if !skill.Model.Valid() {
		return errors.New("モデルは low / middle / high のいずれかです")
	}
	return nil
}
