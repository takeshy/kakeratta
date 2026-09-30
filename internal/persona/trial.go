package persona

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// A trial - a routing check or a reply preview - hands a person a token to
// apply what they were shown. It is kept in the workspace (store.RecordTrials)
// for trialKeep, so the apply may reach any process serving it.
const trialKeep = 15 * time.Minute

// trialRecord is one trial as the workspace keeps it: a routing check or a
// reply preview, and how far applying it has got.
type trialRecord struct {
	Routing *routingTrial `json:"routing,omitempty"`
	Reply   *replyTrial   `json:"reply,omitempty"`
	At      time.Time     `json:"at"`
	// Applying is set while one request applies it; Applied once it has.
	Applying bool `json:"applying,omitempty"`
	Applied  bool `json:"applied,omitempty"`
}

type routingTrial struct {
	EntryID   string `json:"entryId"`
	PersonaID string `json:"personaId"`
	Text      string `json:"text"`
	Delivered bool   `json:"delivered"`
	Reason    string `json:"reason,omitempty"`
	// Heard is the name or keyword a person was found by, kept on the record
	// as a routed delivery would keep it.
	Heard string `json:"heard,omitempty"`
}

type replyTrial struct {
	EntryID   string          `json:"entryId"`
	PersonaID string          `json:"personaId"`
	Original  string          `json:"original"`
	Remark    *timeline.Entry `json:"remark"`
	Notes     []noteChange    `json:"notes,omitempty"`
}

type noteChange struct {
	Path    string             `json:"path"`
	Text    string             `json:"text,omitempty"`
	Sources []store.NoteSource `json:"sources,omitempty"`
	Delete  bool               `json:"delete,omitempty"`
}

func (r *Runner) saveTrial(ctx context.Context, token string, trial trialRecord) error {
	trial.At = time.Now()
	raw, err := json.Marshal(trial)
	if err != nil {
		return err
	}
	return r.store.UpdateRecord(ctx, store.RecordTrials, token, trial.At.Add(trialKeep), func([]byte, bool) ([]byte, error) { return raw, nil })
}

// errTrialApplied is a trial applied already: applying it again does nothing.
var errTrialApplied = errors.New("trial applied already")

// takeTrial marks a trial as being applied, so a second request cannot apply
// it at the same time. The returned done records the outcome: applied, or
// given back for another try.
func (r *Runner) takeTrial(ctx context.Context, token string, expired error) (trialRecord, func(applied bool), error) {
	var trial trialRecord
	err := r.store.UpdateRecord(ctx, store.RecordTrials, token, time.Now().Add(trialKeep), func(current []byte, exists bool) ([]byte, error) {
		if !exists {
			return nil, expired
		}
		if err := json.Unmarshal(current, &trial); err != nil {
			return nil, err
		}
		switch {
		case trial.Applied:
			return nil, errTrialApplied
		case trial.Applying || time.Since(trial.At) > trialKeep:
			return nil, expired
		}
		trial.Applying = true
		return json.Marshal(trial)
	})
	if err != nil {
		return trialRecord{}, nil, err
	}
	done := func(applied bool) {
		next := trial
		next.Applying, next.Applied = false, applied
		raw, _ := json.Marshal(next)
		if err := r.store.UpdateRecord(context.Background(), store.RecordTrials, token, next.At.Add(trialKeep), func([]byte, bool) ([]byte, error) { return raw, nil }); err != nil {
			r.emit("error", map[string]any{"where": "trial", "message": err.Error()})
		}
	}
	return trial, done, nil
}

func (r *Runner) activeSkill(ctx context.Context, settings store.Settings, id string) (Skill, bool) {
	skills := r.skills(ctx).Active(settings.Active)
	index := slices.IndexFunc(skills, func(skill Skill) bool { return skill.ID == id })
	if index < 0 {
		return Skill{}, false
	}
	return skills[index], true
}

// ApplyRouting accepts the router's recorded trial decision. The router is not
// asked again; a newly selected persona gets the post as an ordinary,
// persistent delivery, answered like any other reaction.
func (r *Runner) ApplyRouting(ctx context.Context, token, entryID string) (err error) {
	ctx = r.withSkills(ctx)
	trial, done, err := r.takeTrial(ctx, token, errors.New("振り分けの確認結果が期限切れです。もう一度確認してください"))
	if errors.Is(err, errTrialApplied) {
		return nil
	}
	if err != nil {
		return err
	}
	applied := false
	defer func() { done(applied) }()
	check := trial.Routing
	if check == nil || check.EntryID != entryID {
		return errors.New("投稿が一致しません")
	}
	entry, ok := r.store.Entry(check.EntryID)
	if !ok || entry.Deleted || entry.Text != check.Text {
		return errors.New("投稿が変更されました。もう一度確認してください")
	}
	_, settings := r.clientOf()
	skill, ok := r.activeSkill(ctx, settings, check.PersonaID)
	if !ok {
		return errors.New("その担当は有効ではありません")
	}
	var delivered []string
	err = r.store.UpdateRouting(entry.ID, func(record *store.Routing) (*store.Routing, error) {
		if record == nil || (record.EntryText != nil && *record.EntryText != entry.Text) {
			record = &store.Routing{Delivered: []string{}, Excluded: map[string]string{}, EntryText: &entry.Text, Tracked: true, Done: []string{}, Spoke: []string{}}
		}
		if record.Excluded == nil {
			record.Excluded = map[string]string{}
		}
		// A previous real delivery cannot be undone by a later decision.
		if contains(record.Delivered, skill.ID) {
			return nil, errors.New("この担当にはすでに投稿が届いています")
		}
		if check.Delivered {
			record.Delivered = append(record.Delivered, skill.ID)
			delete(record.Excluded, skill.ID)
			if check.Heard != "" {
				if record.Heard == nil {
					record.Heard = map[string]string{}
				}
				record.Heard[skill.ID] = check.Heard
			}
		} else {
			record.Excluded[skill.ID] = check.Reason
		}
		delivered = record.Delivered
		return record, nil
	})
	if err != nil {
		return err
	}
	r.emit("routed", map[string]any{"entryId": entry.ID, "deliver": delivered})
	if check.Delivered {
		r.accept(store.Reaction{Lane: r.threadLane(entry.ID), Entry: entry.ID, Deliver: skill.ID})
	}
	applied = true
	return nil
}

// ApplyReply keeps the exact approved draft. It never calls the model again.
func (r *Runner) ApplyReply(token, entryID string) error {
	ctx := r.withSkills(context.Background())
	trialRecord, done, err := r.takeTrial(ctx, token, errors.New("試し書きが期限切れです。もう一度試してください"))
	if errors.Is(err, errTrialApplied) {
		return nil
	}
	if err != nil {
		return err
	}
	applied := false
	defer func() { done(applied) }()
	trial := trialRecord.Reply
	if trial == nil || trial.EntryID != entryID {
		return errors.New("投稿が一致しません")
	}
	entry, ok := r.store.Entry(trial.EntryID)
	if !ok || entry.Deleted || entry.Text != trial.Original {
		return errors.New("元の投稿が変更されました。もう一度試してください")
	}
	if trial.Remark == nil {
		return errors.New("返事がない試し書きは確定できません")
	}
	_, settings := r.clientOf()
	skill, ok := r.activeSkill(ctx, settings, trial.PersonaID)
	if !ok {
		return errors.New("その担当は有効ではありません")
	}
	for _, change := range trial.Notes {
		var err error
		if change.Delete {
			err = r.store.DeleteNote(trial.PersonaID, change.Path)
		} else {
			err = r.store.WriteNote(trial.PersonaID, change.Path, change.Text, change.Sources...)
		}
		if err != nil {
			return err
		}
	}
	remark := *trial.Remark
	if entry.Kind == timeline.KindRemark {
		remark.ID, remark.At, remark.Root, remark.ReplyTo = entry.ID, entry.At, entry.Root, entry.ReplyTo
	}
	if err := r.store.Append(remark); err != nil {
		return err
	}
	memory := r.store.MemoryMeta(trial.PersonaID)
	memory.Wakes++
	if err := r.store.SaveMemory(memory); err != nil {
		return err
	}
	if entry.Kind != timeline.KindRemark {
		_ = r.store.RecordWake(timeline.RootOf(entry), trial.PersonaID)
		r.turnsDone(entry.ID, []Skill{skill}, map[string]bool{skill.ID: true})
	}
	r.emit("entry", remark)
	applied = true
	return nil
}
