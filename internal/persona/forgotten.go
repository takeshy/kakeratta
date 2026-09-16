package persona

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

const deletedInstructions = `

## 根拠にしていた投稿が消されました
ユーザーが、あなたがメモの根拠にしていた投稿を削除しました。どの投稿かは対象発言に入っています。
本体はあなたのメモを書き換えません。整理するのはあなたの仕事です。
まず notes_list と notes_read で、その投稿を出典に挙げているメモがあるかを確かめてください。
**無ければ、何もしないで silent。** あなたの記憶はその投稿に依っていません。
あるなら、根拠が無くなった記述を直すか捨ててください。
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

// EntryForgotten tells the personas woken for a deleted post to tidy up.
//
// Who to tell comes from the app's own record of who it woke, never from
// reading a note. A persona that holds the post as a source corrects or drops
// what rested on it; one that holds nothing says nothing.
func (r *Runner) EntryForgotten(entryID, label string) {
	if strings.TrimSpace(entryID) == "" {
		return
	}
	woke, err := r.store.Woke(entryID)
	if err != nil {
		r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
		return
	}
	if len(woke) == 0 {
		return
	}
	_, settings := r.clientOf()
	active := map[string]bool{}
	for _, skill := range r.catalogue.Active(settings.Active) {
		active[skill.ID] = true
	}
	r.stopMu.Lock()
	if r.stopped {
		r.stopMu.Unlock()
		return
	}
	for _, skill := range woke {
		if !active[skill] || r.queuedDeletion(entryID, skill) {
			continue
		}
		r.queue = append(r.queue, reaction{deleted: &deletedPost{entryID: entryID, label: label, skill: skill}})
		r.wake.Signal()
	}
	r.stopMu.Unlock()
	if err := r.store.ForgetWakes(entryID); err != nil {
		r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
	}
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
