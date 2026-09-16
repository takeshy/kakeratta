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

## 根拠にしていた投稿が消されました
ユーザーが、あなたがメモの根拠にしていた投稿を削除しました。どの投稿かは対象発言に入っています。
本体はあなたのメモを書き換えません。整理するのはあなたの仕事です。
その投稿を引用していたメモを notes_read で読み、根拠が無くなった記述を直すか捨ててください。
消えた投稿のリンクは外してください。ほかの根拠が残っている記述は、そのまま残して構いません。
その投稿だけを根拠にしていた概念は、丸ごと捨てるほうが正しいこともあります。
これはユーザーの操作の結果です。消したことを責めたり、理由を尋ねたりしないでください。
伝えることがなければ silent。整理したことをわざわざ報告する必要はありません。
`

// deletedPost is a post the user removed, and who had written it down.
type deletedPost struct {
	entryID string
	label   string
	skill   string
}

// EntryForgotten tells the personas that cited a deleted post to tidy up.
//
// Which personas is decided from the provenance block the app itself writes
// into a note - not from reading what the persona wrote. A persona that never
// cited the post is left alone, because nothing of its memory rests on it.
func (r *Runner) EntryForgotten(entryID, label string) {
	if strings.TrimSpace(entryID) == "" {
		return
	}
	_, settings := r.clientOf()
	for _, skill := range r.catalogue.Active(settings.Active) {
		if !r.cites(skill.ID, entryID) {
			continue
		}
		r.stopMu.Lock()
		if r.stopped {
			r.stopMu.Unlock()
			return
		}
		queued := false
		for _, item := range r.queue {
			if item.deleted != nil && item.deleted.skill == skill.ID && item.deleted.entryID == entryID {
				queued = true
				break
			}
		}
		if !queued {
			r.queue = append(r.queue, reaction{deleted: &deletedPost{entryID: entryID, label: label, skill: skill.ID}})
			r.wake.Signal()
		}
		r.stopMu.Unlock()
	}
}

// cites reports whether one persona wrote this post down as a source.
func (r *Runner) cites(skill, entryID string) bool {
	for _, note := range r.store.Memory(skill).Notes {
		for _, source := range store.NoteSources(note.Text) {
			if source.EntryID == entryID {
				return true
			}
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
	if !known || modelFor(skill.Model, settings) == "" {
		return
	}
	label := strings.Join(strings.Fields(gone.label), " ")
	if runes := []rune(label); len(runes) > 140 {
		label = string(runes[:140]) + "…"
	}
	trigger := timeline.Entry{At: time.Now(), Speaker: "system", Text: fmt.Sprintf(
		"The user deleted the post you cited: %s (/?entry=%s). Tidy the notes that rested on it; nothing was rewritten for you.",
		label, gone.entryID)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	r.run(ctx, skill, []timeline.Entry{trigger}, trigger, false, false, deletedInstructions, client, settings, r.registry(settings))
}
