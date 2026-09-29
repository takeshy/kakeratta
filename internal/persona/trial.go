package persona

import (
	"context"
	"errors"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type routingTrial struct {
	entryID, personaID, text string
	delivered                bool
	reason                   string
	// heard is the name or keyword a person was found by, kept on the record
	// as a routed delivery would keep it.
	heard   string
	at      time.Time
	applied bool
}

type replyTrial struct {
	entryID, personaID, original string
	remark                       *timeline.Entry
	notes                        []noteChange
	at                           time.Time
	applied                      bool
}

type noteChange struct {
	path, text string
	sources    []store.NoteSource
	delete     bool
}

// pruneTrials is called with trialMu held.
func (r *Runner) pruneTrials(now time.Time) {
	for token, trial := range r.routingTrials {
		if now.Sub(trial.at) > 15*time.Minute {
			delete(r.routingTrials, token)
		}
	}
	for token, trial := range r.replyTrials {
		if now.Sub(trial.at) > 15*time.Minute {
			delete(r.replyTrials, token)
		}
	}
}

// ApplyRouting accepts the router's recorded trial decision. The router is not
// asked again; a newly selected persona takes an ordinary, persistent turn.
func (r *Runner) ApplyRouting(ctx context.Context, token, entryID string) error {
	r.trialMu.Lock()
	trial, ok := r.routingTrials[token]
	if ok && !trial.applied {
		delete(r.routingTrials, token)
	}
	r.trialMu.Unlock()
	if !ok || time.Since(trial.at) > 15*time.Minute {
		return errors.New("振り分けの確認結果が期限切れです。もう一度確認してください")
	}
	if trial.entryID != entryID {
		return errors.New("投稿が一致しません")
	}
	if trial.applied {
		return nil
	}
	entry, ok := r.store.Entry(trial.entryID)
	if !ok || entry.Deleted || entry.Text != trial.text {
		return errors.New("投稿が変更されました。もう一度確認してください")
	}
	client, settings := r.clientOf()
	var skill *Skill
	skills := r.catalogue.Active(settings.Active)
	for i := range skills {
		if skills[i].ID == trial.personaID {
			skill = &skills[i]
		}
	}
	if skill == nil {
		return errors.New("その担当は有効ではありません")
	}
	var thread []timeline.Entry
	if trial.delivered {
		entries, err := store.SearchThread(ctx, r.store, entry.ID)
		if err != nil {
			return err
		}
		thread = timeline.Thread(entries, timeline.RootOf(entry))
	}
	r.routingMu.Lock()
	record, err := r.store.Routing(entry.ID)
	if err != nil {
		r.routingMu.Unlock()
		return err
	}
	if record == nil || (record.EntryText != nil && *record.EntryText != entry.Text) {
		record = &store.Routing{Delivered: []string{}, Excluded: map[string]string{}, EntryText: &entry.Text, Tracked: true, Done: []string{}, Spoke: []string{}}
	}
	if record.Excluded == nil {
		record.Excluded = map[string]string{}
	}
	if trial.delivered {
		if contains(record.Delivered, skill.ID) {
			r.routingMu.Unlock()
			return errors.New("この担当にはすでに投稿が届いています")
		}
		record.Delivered = append(record.Delivered, skill.ID)
		delete(record.Excluded, skill.ID)
		if trial.heard != "" {
			if record.Heard == nil {
				record.Heard = map[string]string{}
			}
			record.Heard[skill.ID] = trial.heard
		}
	} else {
		// A previous real delivery cannot be undone by a later decision.
		if contains(record.Delivered, skill.ID) {
			r.routingMu.Unlock()
			return errors.New("この担当にはすでに投稿が届いています")
		}
		record.Excluded[skill.ID] = trial.reason
	}
	err = r.store.SaveRouting(entry.ID, *record)
	r.routingMu.Unlock()
	if err != nil {
		return err
	}
	r.emit("routed", map[string]any{"entryId": entry.ID, "deliver": record.Delivered})
	if !trial.delivered {
		r.markRoutingApplied(token, trial)
		return nil
	}
	pending := r.detaches(ctx, *skill, settings)
	remark := r.run(ctx, *skill, thread, entry, contains(Named(skills, entry.Text), skill.ID), false, "", false, client, settings, r.registry(settings))
	spoke := map[string]bool{}
	if remark != nil {
		spoke[skill.ID] = true
	}
	// A turn waiting on an external planner is marked done when it ends.
	if !pending {
		r.turnsDone(entry.ID, []Skill{*skill}, spoke)
	}
	r.markRoutingApplied(token, trial)
	return nil
}

func (r *Runner) markRoutingApplied(token string, trial routingTrial) {
	trial.applied = true
	r.trialMu.Lock()
	r.routingTrials[token] = trial
	r.trialMu.Unlock()
}

// ApplyReply keeps the exact approved draft. It never calls the model again.
func (r *Runner) ApplyReply(token, entryID string) error {
	r.trialMu.Lock()
	trial, ok := r.replyTrials[token]
	if ok && !trial.applied {
		delete(r.replyTrials, token)
	}
	r.trialMu.Unlock()
	if !ok || time.Since(trial.at) > 15*time.Minute {
		return errors.New("試し書きが期限切れです。もう一度試してください")
	}
	if trial.entryID != entryID {
		return errors.New("投稿が一致しません")
	}
	if trial.applied {
		return nil
	}
	entry, ok := r.store.Entry(trial.entryID)
	if !ok || entry.Deleted || entry.Text != trial.original {
		return errors.New("元の投稿が変更されました。もう一度試してください")
	}
	if trial.remark == nil {
		return errors.New("返事がない試し書きは確定できません")
	}
	_, settings := r.clientOf()
	var skill *Skill
	skills := r.catalogue.Active(settings.Active)
	for i := range skills {
		if skills[i].ID == trial.personaID {
			skill = &skills[i]
		}
	}
	if skill == nil {
		return errors.New("その担当は有効ではありません")
	}
	for _, change := range trial.notes {
		var err error
		if change.delete {
			err = r.store.DeleteNote(trial.personaID, change.path)
		} else {
			err = r.store.WriteNote(trial.personaID, change.path, change.text, change.sources...)
		}
		if err != nil {
			return err
		}
	}
	remark := *trial.remark
	if entry.Kind == timeline.KindRemark {
		remark.ID, remark.At, remark.Root, remark.ReplyTo = entry.ID, entry.At, entry.Root, entry.ReplyTo
	}
	if err := r.store.Append(remark); err != nil {
		return err
	}
	memory := r.store.MemoryMeta(trial.personaID)
	memory.Wakes++
	if err := r.store.SaveMemory(memory); err != nil {
		return err
	}
	if entry.Kind != timeline.KindRemark {
		_ = r.store.RecordWake(timeline.RootOf(entry), trial.personaID)
		r.turnsDone(entry.ID, []Skill{*skill}, map[string]bool{skill.ID: true})
	}
	r.emit("entry", remark)
	trial.applied = true
	r.trialMu.Lock()
	r.replyTrials[token] = trial
	r.trialMu.Unlock()
	return nil
}
