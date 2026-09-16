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
対象発言に、その投稿を引用していたメモの一覧が入っています。空なら、steps を空にして終えてください。あなたの記憶はその投稿に依っていません。
一覧があれば notes_read で読み、根拠が無くなった記述を直すか捨ててください。
**その投稿だけが根拠だった概念のメモは、notes_forget で捨ててください。** 残しても裏づけがありません。
ただし index.md と更新履歴は捨てないでください。消えた項目への参照だけを外し、ファイルは残します。
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
	// notes are the ones that had cited the post, so the persona knows where to
	// look rather than reading everything it has.
	notes []string
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
		// The links are the app's own, so the app takes them out before anyone
		// is woken to look at what is left.
		notes, err := r.store.DropNoteSource(skill, entryID)
		if err != nil {
			r.emit("error", map[string]any{"where": "deleted", "message": err.Error()})
		}
		r.queue = append(r.queue, reaction{deleted: &deletedPost{entryID: entryID, label: label, skill: skill, notes: notes}})
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
	cited := "（なし）"
	if len(gone.notes) > 0 {
		cited = strings.Join(gone.notes, ", ")
	}
	trigger := timeline.Entry{At: time.Now(), Speaker: "system", Text: fmt.Sprintf(
		"The user deleted the post you cited: %s (/?entry=%s). Notes that cited it: %s. Tidy what rested on it; the body is untouched.",
		label, gone.entryID, cited)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// It cannot speak. This wake is bookkeeping after the user deleted a post,
	// and it has no post to answer: a remark here would become a new top-level
	// post of its own, which is the app talking to itself.
	r.run(ctx, skill, []timeline.Entry{trigger}, trigger, false, true, deletedInstructions, client, settings, r.registry(settings))
}
