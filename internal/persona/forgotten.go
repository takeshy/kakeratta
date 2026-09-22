package persona

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

const deletedInstructions = `

## 根拠にしていた投稿が消されました
ユーザーが、あなたがメモの根拠にしていた投稿を削除しました。どの投稿かは対象発言に入っています。
本体はあなたのメモを書き換えません。整理するのはあなたの仕事です。
その投稿だけが根拠だったメモは、本体がすでに削除しました。対象発言にその一覧があります。index.md や他のメモからそれらへの参照を外してください。
対象発言には、ほかの投稿も根拠にしていて残ったメモの一覧も入っています。どちらも空なら、steps を空にして終えてください。
残ったメモは notes_read で読み、消えた投稿に由来する記述を直すか捨ててください。
index.md と更新履歴は捨てないでください。消えた項目への参照だけを外し、ファイルは残します。
ほかの根拠が残っている記述は、そのまま残して構いません。出典のリンクは本体が外してあるので、触らなくてよいです。
**予定も見直してください。** schedule を持っているなら下の一覧を見て、その投稿のために置いた予定は、その id で schedule の cancel を入れて消します。
用そのものが残っているなら、予定はそのままで構いません。
これはユーザーの操作の結果です。消したことを責めたり、理由を尋ねたりしないでください。
このターンは発言できません。整理したことをわざわざ報告する必要はありません。
`

// deletedPost is a post the user removed, and who had written it down.
type deletedPost struct {
	entryID string
	label   string
	skill   string
	// notes are the ones that had cited the post and still stand, so the
	// persona knows where to look rather than reading everything it has.
	notes []string
	// removed are the notes the app deleted because the post was all they
	// rested on, so the persona can take them out of its index.
	removed []string
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
// post, and which notes it linked to it - never from reading a note. The links the app kept from notes to the
// deleted lines are taken out first, and each persona gets one wake for the
// whole deletion rather than one per line. A persona that holds nothing says
// nothing.
func (r *Runner) EntriesForgotten(root string, gone []timeline.Entry) {
	root = strings.TrimSpace(root)
	if root == "" || len(gone) == 0 {
		return
	}
	var ids, labels []string
	rootGone := false
	for _, entry := range gone {
		if entry.ID == root {
			rootGone = true
		}
		// Only the user's lines are ever cited by a note; a persona's remark
		// going with them is nothing a persona wrote down.
		if entry.Speaker != timeline.User {
			continue
		}
		ids = append(ids, entry.ID)
		if text := strings.TrimSpace(entry.Text); text != "" {
			labels = append(labels, text)
		}
	}
	forget := func() {
		if rootGone {
			if err := r.store.ForgetWakes(root); err != nil {
				r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
			}
		}
	}
	if len(ids) == 0 {
		forget()
		return
	}
	woke, err := r.store.Woke(root)
	if err != nil {
		r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
		return
	}
	// A persona can also cite a post it found through timeline_search without
	// having been woken for it; the app's own link index names those.
	for _, skill := range r.catalogue.All() {
		if slices.Contains(woke, skill.ID) {
			continue
		}
		for _, id := range ids {
			if paths, _ := r.store.NotesBySource(skill.ID, id); len(paths) > 0 {
				woke = append(woke, skill.ID)
				break
			}
		}
	}
	if len(woke) == 0 {
		forget()
		return
	}
	_, settings := r.clientOf()
	active := map[string]bool{}
	for _, skill := range r.catalogue.Active(settings.Active) {
		active[skill.ID] = true
	}
	label := strings.Join(labels, " ／ ")
	r.stopMu.Lock()
	if r.stopped {
		r.stopMu.Unlock()
		return
	}
	for _, skill := range woke {
		// The links are the app's own, so the app takes them out before anyone
		// is woken to look at what is left - a paused persona included.
		var notes []string
		seen := map[string]bool{}
		for _, id := range ids {
			dropped, err := r.store.DropNoteSource(skill, id)
			if err != nil {
				r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
			}
			for _, path := range dropped {
				if !seen[path] {
					seen[path] = true
					notes = append(notes, path)
				}
			}
		}
		cited, removed := r.dropUnsourcedNotes(skill, notes)
		if !active[skill] || r.queuedDeletion(root, skill) {
			continue
		}
		r.queue = append(r.queue, reaction{deleted: &deletedPost{entryID: root, label: label, skill: skill, notes: cited, removed: removed}})
		r.wake.Signal()
	}
	r.stopMu.Unlock()
	forget()
}

// dropUnsourcedNotes deletes the notes the deleted lines were the only source
// of. Someone who deletes a post means it gone, so a note that rested on nothing
// else goes with it rather than waiting on the persona to agree. A note with
// other posts behind it stays for the persona to tidy; the index and the log
// are never about one post.
func (r *Runner) dropUnsourcedNotes(skill string, paths []string) (cited, removed []string) {
	for _, path := range paths {
		note, ok := r.store.Note(skill, path)
		if !ok || reservedNote(path) || len(store.NoteSources(note.Text)) > 0 {
			if ok {
				cited = append(cited, path)
			}
			continue
		}
		if err := r.store.DeleteNote(skill, path); err != nil {
			r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
			cited = append(cited, path)
			continue
		}
		removed = append(removed, path)
	}
	return cited, removed
}

// queuedDeletion reports an unprocessed wake for the same post and persona.
// Callers hold stopMu.
func (r *Runner) queuedDeletion(entryID, skill string) bool {
	for _, item := range r.queue {
		if item.deleted != nil && item.deleted.skill == skill && item.deleted.entryID == entryID {
			return true
		}
	}
	return false
}

// deletedAt hands one persona the post that is gone.
func (r *Runner) deletedAt(gone deletedPost) {
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "deleted", "message": fmt.Sprint(problem)})
		}
	}()
	client, settings := r.clientOf()
	if client == nil || client.BaseURL == "" {
		return
	}
	skill, known := r.catalogue.Find(gone.skill)
	if !known || personaModel(settings) == "" {
		return
	}
	label := strings.Join(strings.Fields(gone.label), " ")
	if runes := []rune(label); len(runes) > 140 {
		label = string(runes[:140]) + "…"
	}
	list := func(paths []string) string {
		if len(paths) == 0 {
			return "（なし）"
		}
		return strings.Join(paths, ", ")
	}
	trigger := timeline.Entry{At: time.Now(), Speaker: "system", Text: fmt.Sprintf(
		"The user deleted the post you cited, and whatever was said under it: %s (/?entry=%s). Notes that still cite other posts: %s. Notes the app deleted because they rested only on it: %s. Tidy what rested on it.",
		label, gone.entryID, list(gone.notes), list(gone.removed))}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// It cannot speak. This wake is bookkeeping after the user deleted a post,
	// and it has no post to answer: a remark here would become a new top-level
	// post of its own, which is the app talking to itself.
	r.run(ctx, skill, []timeline.Entry{trigger}, trigger, false, true, deletedInstructions, false, client, settings, r.registry(settings))
}
