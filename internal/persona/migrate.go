package persona

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// Memory used to be one note per thing, with an index.md, kept by the persona
// itself. MigrateMemory folds those notes into the fields once, by the
// persona's model, and throws them away. It is not part of the running app: it
// is run by hand (cmd/migrate-memory) after the deploy that brought the
// fields, and the running app reads nothing but fields and records. The posts
// the old notes rested on are still on the timeline; what is lost is their
// wording, not where it came from. No records are made from old notes.

// maxMigrateRunes is how much of the old notes one migration reads. The most
// recently written come first, so what is left out is the oldest.
const maxMigrateRunes = 60000

// Migration is what MigrateMemory found for one persona and what it made of it.
type Migration struct {
	Persona string `json:"persona"`
	// Legacy are the old notes, every one of which goes when applied.
	Legacy []string `json:"legacy"`
	// Omitted are the old notes left out of what the model read, the oldest,
	// for want of room.
	Omitted []string `json:"omitted,omitempty"`
	// Fields are the summaries the model wrote, by field, whole.
	Fields  map[string]string `json:"fields"`
	Applied bool              `json:"applied"`
}

func migratePrompt(skill Skill) string {
	s := fmt.Sprintf("%s%s\n担当領域: %s\n担当の指示: %s\n", skill.intro(), skill.Tone, skill.Domain, skill.Instructions)
	s += "いまはこの担当のメモ係として、記憶の持ち方を切り替える。これまでは対象ごとに1つずつメモを持っていたが、これからは分野ごとのまとめだけを持つ。\n"
	s += "分野: " + strings.Join(memoryFields(skill), " / ") + "\n"
	if memory := strings.TrimSpace(skill.forTurn().Memory); memory != "" {
		s += "分野の分け方と残し方: " + memory + "\n"
	}
	s += `渡された古いメモをすべて読み、keep_memory の fields で分野ごとのまとめの全文を出す。古いメモは、このあと本体が捨てる。
- まとめは、その分野でこれまでに分かったことの要約で、` + fmt.Sprint(maxFieldRunes) + `字以内。いまのまとめがあれば、その内容も落とさない。
- 移すのはユーザーが言ったこと・したことだけ（事実・好み・予定・関係の変化・行きたい／読みたいと言ったもの・覚えてと頼まれたこと）。贈った・会った・行った・買った・飲んだなど、ユーザーの行動や出来事は日付とともに必ず残す。
- **担当が調べて答えた内容は移さない。** 数値（含有量・価格・距離・所要時間・評価など）、解説や手順、レシピの分量、商品や店の詳しい説明、候補の一覧、ニュースや技術の調査結果、担当の見立ては、古いメモに書いてあっても捨てる。必要になれば、そのときに調べ直す。
- ユーザーが何について聞いたかは、傾向として一言にまとめてよい（例: 「プリン体をよく気にして食べ物の含有量を聞く」）。個々の質問の答えは書かない。
- 期限付きの一時的なメモ、その場限りの相談は移さない。
- 収まらないときは、細部より傾向を残す。
- 引用、本人の意見、推測を区別し、推測は推測と明記する。投稿のIDやリンク、frontmatter は書かない。
- 古いメモと同じ言語で書く（日本語のメモなら日本語）。
- 移すことがない分野は fields に入れない。
`
	return s
}

// MigrateMemory folds one persona's old notes into its fields. Without apply
// it only reports what it would write; with apply it writes the fields and
// deletes every old note, under the persona's keeper lock. A persona with no
// old notes is reported with none, and the model is not called. To write
// exactly what a dry run showed, pass that to ApplyMigration instead.
func (r *Runner) MigrateMemory(ctx context.Context, personaID string, apply bool) (Migration, error) {
	out := Migration{Persona: personaID, Fields: map[string]string{}}
	skill, ok := r.skills(ctx).Find(personaID)
	if !ok {
		return out, fmt.Errorf("担当が見つかりません: %s", personaID)
	}
	skill = skill.forTurn()
	held, release, err := r.lockKeeper(ctx, skill.ID)
	if err != nil {
		return out, err
	}
	defer release()
	legacy, err := r.legacyNotes(skill)
	if err != nil || len(legacy) == 0 {
		return out, err
	}
	sort.SliceStable(legacy, func(i, j int) bool { return legacy[i].Written.After(legacy[j].Written) })
	var parts []string
	used := 0
	for _, note := range legacy {
		out.Legacy = append(out.Legacy, note.Path)
		text, _ := splitSources(note.Text)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		part := "=== " + note.Path + " ===\n" + text
		if used+len([]rune(part)) > maxMigrateRunes {
			out.Omitted = append(out.Omitted, note.Path)
			continue
		}
		used += len([]rune(part))
		parts = append(parts, part)
	}
	fields := memoryFields(skill)
	if len(parts) > 0 {
		client, settings := r.clientOf()
		model := personaModel(settings)
		if client == nil || client.BaseURL == "" || model == "" {
			return out, fmt.Errorf("モデルの接続がありません")
		}
		user := "いまの分野ごとのまとめ（全文）:\n" + renderFields(skill, r.fieldBodies(skill)) +
			"\n\n古いメモ（新しく書かれた順）:\n" + strings.Join(parts, "\n\n")
		kept, _, err := submitMemory(held, client, model, migratePrompt(skill), user, keepMemoryTool(fields, false, nil), func(kept keptMemory) []string {
			return checkKept(kept, fields, false, nil, "", time.Now(), time.UTC)
		})
		if err != nil {
			return out, err
		}
		for _, write := range kept.Fields {
			field, _ := fieldNamed(fields, write.Field)
			out.Fields[field] = repairNewlines(strings.TrimSpace(write.Text))
		}
		if apply {
			if _, err := r.writeFields(held, skill, fields, kept.Fields); err != nil {
				return out, err
			}
		}
	}
	if !apply {
		return out, nil
	}
	for _, note := range legacy {
		if err := held.Err(); err != nil {
			return out, err
		}
		if err := r.store.DeleteNote(skill.ID, note.Path); err != nil {
			return out, err
		}
	}
	out.Applied = true
	return out, nil
}

// ApplyMigration writes a migration a dry run made, as it is (or as a person
// edited it), without calling the model again: the fields it holds are
// written and its old notes deleted. It refuses when the persona's old notes
// are no longer the ones the dry run read, or a field is unknown or too long.
func (r *Runner) ApplyMigration(ctx context.Context, plan Migration) (Migration, error) {
	skill, ok := r.skills(ctx).Find(plan.Persona)
	if !ok {
		return plan, fmt.Errorf("担当が見つかりません: %s", plan.Persona)
	}
	skill = skill.forTurn()
	held, release, err := r.lockKeeper(ctx, skill.ID)
	if err != nil {
		return plan, err
	}
	defer release()
	legacy, err := r.legacyNotes(skill)
	if err != nil {
		return plan, err
	}
	now := map[string]bool{}
	for _, note := range legacy {
		now[note.Path] = true
	}
	if len(now) != len(plan.Legacy) {
		return plan, fmt.Errorf("%s: 古いメモが dry-run のときから変わっています（%d件 → %d件）", plan.Persona, len(plan.Legacy), len(now))
	}
	for _, path := range plan.Legacy {
		if !now[path] {
			return plan, fmt.Errorf("%s: 古いメモが dry-run のときから変わっています（%s がありません）", plan.Persona, path)
		}
	}
	fields := memoryFields(skill)
	var writes []fieldWrite
	for field, text := range plan.Fields {
		writes = append(writes, fieldWrite{Field: field, Text: text})
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].Field < writes[j].Field })
	if problems := checkKept(keptMemory{Fields: writes}, fields, false, nil, "", time.Now(), time.UTC); len(problems) > 0 {
		return plan, fmt.Errorf("%s: %s", plan.Persona, strings.Join(problems, "; "))
	}
	if _, err := r.writeFields(held, skill, fields, writes); err != nil {
		return plan, err
	}
	for _, note := range legacy {
		if err := held.Err(); err != nil {
			return plan, err
		}
		if err := r.store.DeleteNote(skill.ID, note.Path); err != nil {
			return plan, err
		}
	}
	plan.Applied = true
	return plan, nil
}

// MigrationPersonas are the ids of every persona defined, active or not: a
// paused persona's old notes are folded too.
func (r *Runner) MigrationPersonas(ctx context.Context) []string {
	var ids []string
	for _, skill := range r.skills(ctx).All() {
		ids = append(ids, skill.ID)
	}
	return ids
}

// splitSources is a note's text without the post links the app kept on it.
func splitSources(text string) (string, []store.NoteSource) {
	sources := store.NoteSources(text)
	if start := strings.LastIndex(text, "<!-- kakeratta:note-sources -->"); start >= 0 {
		text = text[:start]
	}
	return text, sources
}
