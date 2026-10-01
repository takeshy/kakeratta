package persona

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Fields were once kept as prose, rewritten whole by the keeper. MigrateMemory
// splits a persona's prose fields into facts, once, by the persona's model. It
// is not part of the running app, which reads only fact lines: it is run by
// hand (cmd/migrate-memory) after the deploy that brought the facts. A dry run
// shows the facts and saves them as a plan; ApplyMigration writes the plan, as
// a person may have edited it. The prose stays as the field's summary; the
// facts carry no kakera, since the prose never said which one it rested on.

// Migration is what MigrateMemory found for one persona and what it made of it.
type Migration struct {
	Persona string `json:"persona"`
	// Legacy are the prose field files, every one of which is replaced or,
	// for a field the layout no longer names, deleted when applied.
	Legacy []string `json:"legacy"`
	// Facts are the facts the model wrote, by field.
	Facts map[string][]string `json:"facts"`
	// Summaries are the prose kept as each field's summary, by field; a field
	// the layout no longer names keeps none.
	Summaries map[string]string `json:"summaries"`
	// Prose is each prose file as the dry run read it, so applying refuses
	// one written since - the running app may have rewritten it meanwhile.
	Prose   map[string]string `json:"prose"`
	Applied bool              `json:"applied"`
}

func migratePrompt(skill Skill) string {
	s := fmt.Sprintf("%s%s\n担当領域: %s\n担当の指示: %s\n", skill.intro(), skill.Tone, skill.Domain, skill.Instructions)
	s += "いまはこの担当のメモ係として、記憶の持ち方を切り替える。これまでは分野ごとにまとめの文章を持っていたが、これからは分野ごとの事実の一覧を持つ。\n"
	s += "分野: " + strings.Join(memoryFields(skill), " / ") + "\n"
	if memory := strings.TrimSpace(skill.forTurn().Memory); memory != "" {
		s += "分野の分け方と残し方: " + memory + "\n"
	}
	s += `渡されたまとめの文章を読み、keep_memory の facts に、事実を1件ずつ add で出す。まとめの文章は、このあと本体が事実の一覧に置き換える。
- 1件は1つのこと、` + fmt.Sprint(maxFactRunes) + `字以内。まとめに書いてあることを落とさず、足さない。日付があれば残す。
- 分野の設定から外れた分野のまとめは、合う分野へ入れる。
- 分野ごとの事実は合わせて` + fmt.Sprint(maxFieldRunes) + `字まで。
- まとめと同じ言語で書く。
`
	return s
}

// MigrateMemory splits one persona's prose fields into facts. It changes
// nothing: to write them, pass the result to ApplyMigration. A persona with no
// prose fields is reported with none, and the model is not called.
func (r *Runner) MigrateMemory(ctx context.Context, personaID string) (Migration, error) {
	out := Migration{Persona: personaID, Facts: map[string][]string{}, Summaries: map[string]string{}, Prose: map[string]string{}}
	skill, ok := r.skills(ctx).Find(personaID)
	if !ok {
		return out, fmt.Errorf("担当が見つかりません: %s", personaID)
	}
	skill = skill.forTurn()
	prose, err := r.proseFields(skill)
	if err != nil || len(prose) == 0 {
		return out, err
	}
	var parts []string
	fields := memoryFields(skill)
	for _, note := range prose {
		out.Legacy = append(out.Legacy, note.Path)
		out.Prose[note.Path] = note.Text
		field := strings.TrimSuffix(note.Path, ".md")
		parts = append(parts, "=== "+field+" ===\n"+strings.TrimSpace(note.Text))
		if contains(fields, field) {
			out.Summaries[field] = strings.TrimSpace(note.Text)
		}
	}
	client, settings := r.clientOf()
	model := personaModel(settings)
	if client == nil || client.BaseURL == "" || model == "" {
		return out, fmt.Errorf("モデルの接続がありません")
	}
	empty := memoryFacts{Fields: fields, ByField: map[string]fieldFacts{}}
	kept, _, err := submitMemory(ctx, client, model, migratePrompt(skill), "分野ごとのまとめ（全文）:\n"+strings.Join(parts, "\n\n"), keepMemoryTool(fields, false, nil), func(kept keptMemory) []string {
		var problems []string
		for i, op := range kept.Facts {
			if op.Op != "add" {
				problems = append(problems, fmt.Sprintf("facts[%d]: ここでは add だけを使ってください", i))
			}
		}
		_, _, more := planFacts(empty, kept.Facts, "", time.Now(), time.UTC)
		return append(problems, more...)
	})
	if err != nil {
		return out, err
	}
	for _, op := range kept.Facts {
		field, _ := fieldNamed(fields, op.Field)
		out.Facts[field] = append(out.Facts[field], cleanFact(op.Text))
	}
	return out, nil
}

// ApplyMigration writes a migration a dry run made, as it is or as a person
// edited it, without calling the model again: each field becomes its facts,
// and a prose field the layout no longer names is deleted. It refuses when
// the persona's prose fields are no longer the ones the dry run read, or a
// fact is too long or a field too full.
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
	prose, err := r.proseFields(skill)
	if err != nil {
		return plan, err
	}
	now := map[string]bool{}
	for _, note := range prose {
		now[note.Path] = true
		if read, ok := plan.Prose[note.Path]; ok && read != note.Text {
			return plan, fmt.Errorf("%s: %s が dry-run のときから書き換わっています。dry-run をやり直してください", plan.Persona, note.Path)
		}
	}
	if len(now) != len(plan.Legacy) {
		return plan, fmt.Errorf("%s: まとめが dry-run のときから変わっています（%d件 → %d件）", plan.Persona, len(plan.Legacy), len(now))
	}
	for _, path := range plan.Legacy {
		if !now[path] {
			return plan, fmt.Errorf("%s: まとめが dry-run のときから変わっています（%s がありません）", plan.Persona, path)
		}
	}
	_, settings := r.clientOf()
	zone := settings.Location()
	fields := memoryFields(skill)
	m := memoryFacts{Fields: fields, ByField: map[string]fieldFacts{}}
	var ops []factOp
	var names []string
	for field := range plan.Facts {
		names = append(names, field)
	}
	sort.Strings(names)
	for _, field := range names {
		for _, text := range plan.Facts[field] {
			ops = append(ops, factOp{Op: "add", Field: field, Text: text})
		}
	}
	next, changed, problems := planFacts(m, ops, "", time.Now(), zone)
	today := localDay(time.Now(), zone)
	for field, summary := range plan.Summaries {
		field, ok := fieldNamed(fields, field)
		summary = strings.TrimSpace(summary)
		if !ok || summary == "" {
			continue
		}
		if n := len([]rune(summary)); n > maxFieldRunes {
			problems = append(problems, fmt.Sprintf("分野「%s」のまとめ文が%d字あります", field, n))
		}
		ff := next.ByField[field]
		ff.Summary, ff.SummaryAt = summary, today
		next.ByField[field] = ff
		if !contains(changed, field) {
			changed = append(changed, field)
		}
	}
	if len(problems) > 0 {
		return plan, fmt.Errorf("%s: %s", plan.Persona, strings.Join(problems, "; "))
	}
	if _, err := r.writeFacts(held, skill.ID, next, changed, zone); err != nil {
		return plan, err
	}
	for _, note := range prose {
		if err := held.Err(); err != nil {
			return plan, err
		}
		field := strings.TrimSuffix(note.Path, ".md")
		if contains(changed, field) {
			continue // Rewritten as facts.
		}
		if err := r.store.DeleteNote(skill.ID, note.Path); err != nil {
			return plan, err
		}
	}
	plan.Applied = true
	return plan, nil
}

// MigrationPersonas are the ids of every persona defined, active or not: a
// paused persona's fields are split too.
func (r *Runner) MigrationPersonas(ctx context.Context) []string {
	var ids []string
	for _, skill := range r.skills(ctx).All() {
		ids = append(ids, skill.ID)
	}
	return ids
}
