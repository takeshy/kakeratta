package persona

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

const deletedInstructions = `

## かけらが消されました
ユーザーが、あなたが記録していたかけらを削除しました。どのかけらかは対象発言に入っています。
その記録と、そのかけらだけを根拠にしていた事実は、本体がすでに消しました。
**予定も見直してください。** schedule を持っているなら下の一覧を見て、そのかけらのために置いた予定は、その id で schedule の cancel を入れて消します。
用そのものが残っているなら、予定はそのままで構いません。
これはユーザーの操作の結果です。消したことを責めたり、理由を尋ねたりしないでください。
このターンは発言できません。整理したことをわざわざ報告する必要はありません。
`

// tidiesUp reports a wake that only puts memory back in order after the user
// deleted a post. It cannot speak and has nothing to answer, so it skips the
// research and the answer and goes straight to the schedule phase; the app
// has already taken out the records and facts that rested on the post. It is never handed to the external agent, which would only be
// asked for an answer nobody will see.
func tidiesUp(instructions string) bool {
	return instructions == deletedInstructions
}

// deletedPost is a post the user removed, and who had recorded it.
type deletedPost struct {
	entryID string
	label   string
	skill   string
}

// EntryForgotten tells the personas woken for a deleted entry to tidy up. A
// deleted post takes the conversation under it along; the server hands those
// over together through EntriesForgotten, and this is the one-entry case.
func (r *Runner) EntryForgotten(entryID, label string) {
	if strings.TrimSpace(entryID) == "" {
		return
	}
	entry, ok := r.store.Entry(entryID)
	if !ok {
		entry = timeline.Entry{ID: entryID, Kind: timeline.KindPost, Speaker: timeline.User, Root: entryID}
	}
	if strings.TrimSpace(label) != "" {
		entry.Text = label
	}
	r.EntriesForgotten(timeline.RootOf(entry), []timeline.Entry{entry})
}

// EntriesForgotten tells the personas woken for a post that some of it is
// gone: the post itself and everything said under it, or one line of the
// user's. Who to tell comes from the app's own records - who it woke for the
// post - never from reading a body. The records kept under the deleted lines
// (the kakera, or the remark answering it) are taken out first, and each
// persona that had any gets one wake for the whole deletion to take out of its
// fields what rested on them.
func (r *Runner) EntriesForgotten(root string, gone []timeline.Entry) {
	ctx := r.withSkills(context.Background())
	root = strings.TrimSpace(root)
	if root == "" || len(gone) == 0 {
		return
	}
	var labels []string
	rootGone := false
	for _, entry := range gone {
		if entry.ID == root {
			rootGone = true
		}
		if entry.Speaker != timeline.User {
			continue
		}
		if text := strings.TrimSpace(entry.Text); text != "" {
			labels = append(labels, text)
		}
	}
	defer func() {
		if rootGone {
			if err := r.store.ForgetWakes(root); err != nil {
				r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
			}
		}
	}()
	_, settings := r.clientOf()
	zone := settings.Location()
	if len(labels) == 0 {
		// Only replies of the personas went: their records are kept under
		// the kakera they answered, and nobody has anything to tidy.
		seen := map[string]bool{}
		for _, entry := range gone {
			if entry.Speaker == timeline.User || entry.Speaker == "system" || seen[entry.Speaker] {
				continue
			}
			seen[entry.Speaker] = true
			if _, err := r.dropRecords(ctx, entry.Speaker, gone, zone); err != nil {
				r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
			}
		}
		return
	}
	woke, err := r.store.Woke(root)
	if err != nil {
		r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
		return
	}
	active := map[string]bool{}
	for _, skill := range r.skills(ctx).Active(settings.Active) {
		active[skill.ID] = true
	}
	label := strings.Join(labels, " ／ ")
	kakera := map[string]bool{}
	for _, entry := range gone {
		if entry.Speaker == timeline.User {
			kakera[entry.ID] = true
		}
	}
	for _, skill := range woke {
		// The records and the facts' sources are the app's own, so the app
		// takes them out before anyone is woken - a paused persona included.
		dropped, err := r.dropRecords(ctx, skill, gone, zone)
		if err != nil {
			r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
		}
		if persona, ok := r.skills(ctx).Find(skill); ok {
			if _, err := r.dropFactSources(ctx, persona.forTurn(), kakera, zone); err != nil {
				r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
			}
		}
		if !dropped || !active[skill] {
			continue
		}
		// Queued twice for the same post, the wakes are folded into one.
		r.accept(store.Reaction{Lane: "thread:" + root, Deleted: &store.DeletedReaction{EntryID: root, Label: label, Skill: skill}})
	}
}

// deletedAt hands one persona the post that is gone.
func (r *Runner) deletedAt(ctx context.Context, gone deletedPost) {
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "deleted", "message": fmt.Sprint(problem)})
		}
	}()
	client, settings := r.clientOf()
	if client == nil || client.BaseURL == "" {
		return
	}
	skill, known := r.skills(ctx).Find(gone.skill)
	if !known || personaModel(settings) == "" {
		return
	}
	label := strings.Join(strings.Fields(gone.label), " ")
	if runes := []rune(label); len(runes) > 140 {
		label = string(runes[:140]) + "…"
	}
	trigger := timeline.Entry{At: time.Now(), Speaker: "system", Text: fmt.Sprintf(
		"The user deleted a kakera you had recorded, and whatever was said under it: %s (%s). Its records are gone; take out of your fields what rested on it.",
		label, gone.entryID)}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	// It cannot speak. This wake is bookkeeping after the user deleted a post,
	// and it has no post to answer: a remark here would become a new top-level
	// post of its own, which is the app talking to itself.
	r.run(withBuiltinPlanner(ctx), skill, []timeline.Entry{trigger}, trigger, false, true, deletedInstructions, false, client, settings, r.registry(settings))
}
