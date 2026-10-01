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
- 中心はユーザーが言ったこと（事実・好み・予定・関係の変化・覚えてと頼まれたこと）。調べた事実・候補一覧・解説・期限付きの一時的なメモは移さない。
- 収まらないときは、細部より傾向を残す。
- 引用、本人の意見、推測を区別し、推測は推測と明記する。投稿のIDやリンク、frontmatter は書かない。
- 移すことがない分野は fields に入れない。
`
	return s
}

// MigrateMemory folds one persona's old notes into its fields. Without apply
// it only reports what it would write; with apply it writes the fields and
// deletes every old note, under the persona's keeper lock. A persona with no
// old notes is reported with none, and the model is not called.
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
			out.Fields[field] = strings.TrimSpace(write.Text)
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
