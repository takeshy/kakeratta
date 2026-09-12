// Package persona is the reading companions: who they are, and what they are
// allowed to see.
//
// A plugin declares a personality and whether it needs the web. It does not
// declare what of the book it can read, because that is not its to decide - the
// book's category already settled it. The point is not that a persona is told
// to respect the boundary; it is that a novel persona is never handed anything
// past it, so there is nothing to respect.
package persona

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/takeshy/dokking/internal/book"
)

// Plugin is everything a companion declares about itself.
//
// Note what is absent: any mention of text access, retrieval, or the boundary.
// The only outward-facing thing here is WebSearch, because leaving the machine
// is the one thing worth asking the reader about.
type Plugin struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Icon       string          `json:"icon"`
	Tone       string          `json:"tone"`
	Categories []book.Category `json:"categories"`
	WebSearch  bool            `json:"webSearch"`
	Builtin    bool            `json:"builtin"`
}

// Suits reports whether this persona can be put in a book of this category.
func (p Plugin) Suits(category book.Category) bool {
	for _, allowed := range p.Categories {
		if allowed == category {
			return true
		}
	}
	return false
}

var idPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// Builtins ship with the app so the reader has someone to read with before
// they have installed anything.
var Builtins = []Plugin{
	{
		ID: "mikage", Name: "みかげ", Icon: "🔎",
		Tone: "疑り深い読者。細かい言い回しやつじつまの合わなさを覚えていて、" +
			"ずっと後になってから「さっきの話、前に言っていたことと違わない?」と切り出す。" +
			"口数は少なく、確信がないときは黙っている。断定せず、気になったことだけを短く言う。",
		Categories: []book.Category{book.CategoryNovel}, Builtin: true,
	},
	{
		ID: "nagi", Name: "なぎ", Icon: "🌙",
		Tone: "のんびりした読書仲間。人物の気持ちや情景に反応する。" +
			"解釈を押しつけず、「自分はこう読んだ」という言い方をする。" +
			"読者がメモを書いたときに、別の読み方をひとつだけ添えることがある。",
		Categories: []book.Category{book.CategoryNovel}, Builtin: true,
	},
	{
		ID: "koyomi", Name: "こよみ", Icon: "📡",
		Tone: "技術書を一緒に読む相棒。本が書かれた後に変わったことを気にかけている。" +
			"バージョン番号・製品名・API名・非推奨化にだけ反応し、それ以外では黙っている。" +
			"必ず検索結果を根拠にし、出典のリンクを添える。記憶から固有名詞やバージョンを出さない。",
		Categories: []book.Category{book.CategoryTechnical}, WebSearch: true, Builtin: true,
	},
}

// Catalogue is the built-in personas plus whatever the reader installed.
type Catalogue struct{ root string }

// NewCatalogue reads installed plugins from <workspace>/personas.
func NewCatalogue(root string) *Catalogue {
	return &Catalogue{root: filepath.Join(root, "personas")}
}

// All lists every persona available, built-ins first.
func (c *Catalogue) All() []Plugin {
	out := append([]Plugin(nil), Builtins...)
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		plugin, err := c.read(entry.Name())
		if err != nil {
			continue
		}
		out = append(out, plugin)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// For lists the personas that fit a category. Choosing the category first is
// what makes this list short enough to choose from.
func (c *Catalogue) For(category book.Category) []Plugin {
	var out []Plugin
	for _, plugin := range c.All() {
		if plugin.Suits(category) {
			out = append(out, plugin)
		}
	}
	return out
}

// Find returns one persona by id.
func (c *Catalogue) Find(id string) (Plugin, bool) {
	for _, plugin := range c.All() {
		if plugin.ID == id {
			return plugin, true
		}
	}
	return Plugin{}, false
}

func (c *Catalogue) read(id string) (Plugin, error) {
	raw, err := os.ReadFile(filepath.Join(c.root, id, "persona.json"))
	if err != nil {
		return Plugin{}, err
	}
	var plugin Plugin
	if err := json.Unmarshal(raw, &plugin); err != nil {
		return Plugin{}, err
	}
	if err := validate(plugin); err != nil {
		return Plugin{}, err
	}
	if plugin.ID != id {
		return Plugin{}, errors.New("persona id does not match its directory")
	}
	plugin.Builtin = false
	return plugin, nil
}

// Install writes a persona manifest into the workspace.
//
// It is validated before anything is written, and the id has to be a plain
// name: a plugin is a file the reader was given by someone else, so it decides
// nothing about where it lands.
func (c *Catalogue) Install(raw []byte) (Plugin, error) {
	if len(raw) > 256*1024 {
		return Plugin{}, errors.New("ペルソナの定義が大きすぎます")
	}
	var plugin Plugin
	if err := json.Unmarshal(raw, &plugin); err != nil {
		return Plugin{}, errors.New("ペルソナの定義を読めませんでした")
	}
	if err := validate(plugin); err != nil {
		return Plugin{}, err
	}
	for _, builtin := range Builtins {
		if builtin.ID == plugin.ID {
			return Plugin{}, errors.New("同じ名前の内蔵ペルソナがあります")
		}
	}
	plugin.Builtin = false
	dir := filepath.Join(c.root, plugin.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Plugin{}, err
	}
	encoded, err := json.MarshalIndent(plugin, "", "  ")
	if err != nil {
		return Plugin{}, err
	}
	temp := filepath.Join(dir, "persona.json.tmp")
	if err := os.WriteFile(temp, encoded, 0o644); err != nil {
		return Plugin{}, err
	}
	if err := os.Rename(temp, filepath.Join(dir, "persona.json")); err != nil {
		return Plugin{}, err
	}
	return plugin, nil
}

// Uninstall removes an installed persona. What it wrote in books stays: the
// reader may put it back, and it should come back as the one that was reading
// along.
func (c *Catalogue) Uninstall(id string) error {
	if !idPattern.MatchString(id) {
		return errors.New("ペルソナのIDが不正です")
	}
	for _, builtin := range Builtins {
		if builtin.ID == id {
			return errors.New("内蔵ペルソナは削除できません")
		}
	}
	return os.RemoveAll(filepath.Join(c.root, id))
}

func validate(plugin Plugin) error {
	if !idPattern.MatchString(plugin.ID) {
		return errors.New("ペルソナのIDは英小文字・数字・ハイフンだけで書いてください")
	}
	if strings.TrimSpace(plugin.Name) == "" || len([]rune(plugin.Name)) > 40 {
		return errors.New("ペルソナの名前が不正です")
	}
	if strings.TrimSpace(plugin.Tone) == "" || len([]rune(plugin.Tone)) > 2000 {
		return errors.New("ペルソナの性格の記述が不正です")
	}
	if len([]rune(plugin.Icon)) > 4 {
		return errors.New("アイコンは絵文字1つにしてください")
	}
	if len(plugin.Categories) == 0 {
		return errors.New("対象カテゴリを1つ以上宣言してください")
	}
	for _, category := range plugin.Categories {
		if !category.Valid() {
			return errors.New("対象カテゴリは novel か technical です")
		}
	}
	return nil
}
