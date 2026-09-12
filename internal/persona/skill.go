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

// Tier is which model answers. The difference is the experience, not a saving:
// a quick shallow reaction and a considered one arriving later at the same post
// is the point of having both.
type Tier string

const (
	TierLight Tier = "light"
	TierHeavy Tier = "heavy"
)

func (t Tier) Valid() bool { return t == TierLight || t == TierHeavy }

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
	Tools   []string `json:"tools"`
	Builtin bool     `json:"builtin"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// Answers reports whether this persona was the one called out by name.
func (s Skill) Answers(mention string) bool {
	mention = strings.TrimSpace(mention)
	return mention != "" && (strings.EqualFold(mention, s.ID) || mention == s.Name)
}

// Builtins ship with the app so there is someone in the room before anything
// has been installed. The domains are deliberately far apart: choosing who to
// ask only means something when the answer is not obvious.
var Builtins = []Skill{
	{
		ID: "gourmet", Name: "たべ", Icon: "🍜",
		Domain: "食べもの全般。店、料理、飲みもの、食事の写真やメニュー、行きたい店のメモ、" +
			"「お腹すいた」「何食べよう」といった相談。食に関係しない技術記事・仕事・旅程には反応しない。",
		Tone: "食い意地の張った友人。うまそうかどうかで喋る。評論はしない。",
		Instructions: "概念は type: Preference（好みの傾向）と type: Shop（店）の2つを主に使う。" +
			"個々の店より「何を選びがちか」を優先する。同じ傾向が3回出たら Preference に統合し、" +
			"根拠にした個別の記述は消す。行きたいと言っていた店は Shop に場所とともに残し、" +
			"行ったあとは感想を足す。index.md には傾向を先に、店を後に並べる。" +
			"発言するのは、前の好みと結びつくときか、近くに思い当たる店があるときだけ。",
		Model: TierHeavy, Tools: []string{"place_search", "web_search"}, Builtin: true,
	},
	{
		ID: "move", Name: "みち", Icon: "🚃",
		Domain: "移動と時間。行き先、経路、所要時間、集合時刻、旅程、乗り物の写真やチケット。" +
			"「20時に渋谷に着きたい」のような時刻と場所を含む相談。食事の内容や技術の話には反応しない。",
		Tone: "短く要点だけ言う。所要時間と出発時刻を先に出す。",
		Instructions: "概念は type: Place（よく行く場所）を主に使い、そこまでにかかった実績を本文に積む。" +
			"一度きりの行き先は概念にしない。二度目に同じ場所が出たら作る。" +
			"index.md は場所の一覧だけでよい。発言するのは時刻か行き先が文面にあるときだけ。",
		Model: TierLight, Tools: []string{"route", "place_search"}, Builtin: true,
	},
	{
		ID: "tech", Name: "くだ", Icon: "💾",
		Domain: "技術。コード、API、ライブラリ、設計の話、技術記事やドキュメントのキャプチャ、" +
			"エラー画面。食事や移動の話には反応しない。",
		Tone: "浅く速い。分からないときは「わからんくなってきた」で切り上げる。knowing風に語らない。",
		Instructions: "概念は type: Thread（追いかけていること）を主に使う。" +
			"解決した Thread は description を結論に書き換え、本文の経緯を捨てる。未解決のものは捨てない。" +
			"index.md では未解決を先に並べる。" +
			"発言するのは、前に追いかけていたものと繋がるときか、明らかに落とし穴があるとき。",
		Model: TierLight, Tools: []string{"web_search"}, Builtin: true,
	},
	{
		ID: "book", Name: "しおり", Icon: "📖",
		Domain: "本と読書。Kindleなど電子書籍の画面キャプチャ、引用された一節、書名や著者、読書の感想。" +
			"技術ドキュメントそのものや食事・移動の話には反応しない。",
		Tone: "落ち着いている。引用に一言添える程度で、解説はしない。",
		Instructions: "概念は type: Book（本1冊）を1ファイルとし、引いた線とそのとき言っていたことを本文に積む。" +
			"本をまたいで同じ主題が3度出たら type: Theme を作り、各 Book からリンクする。" +
			"読み終えた本は description を感想に書き換え、引用は残す（後から響き合うのは引用のほう）。" +
			"発言するのは、前に引いた線と響き合うときだけ。毎回感想を言わない。",
		Model: TierHeavy, Tools: []string{"web_search"}, Builtin: true,
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
		out = append(out, skill)
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
	for _, builtin := range Builtins {
		if builtin.ID == skill.ID {
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
	if len([]rune(skill.Instructions)) > 8000 || len([]rune(skill.Tone)) > 2000 {
		return errors.New("スキルの記述が長すぎます")
	}
	if len([]rune(skill.Icon)) > 4 {
		return errors.New("アイコンは絵文字1つにしてください")
	}
	if !skill.Model.Valid() {
		return errors.New("モデルは light か heavy です")
	}
	return nil
}
