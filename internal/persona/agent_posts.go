package persona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// MaxAgentPostsPerDay is how many posts the external agent may make as one
// persona in a day of the user's time zone.
const MaxAgentPostsPerDay = 5

// ErrAgentPostsExhausted is a post as a persona past the day's limit.
var ErrAgentPostsExhausted = fmt.Errorf("このペルソナとして投稿できるのは1日%d件までです", MaxAgentPostsPerDay)

// ErrNotAgentPersona is a post as a persona that is not active or does not
// use the external agent, or while the agent is paused.
var ErrNotAgentPersona = errors.New("有効で、外部エージェントを使う設定のペルソナではありません")

// PostAsPersona posts the external agent's text as a new post by an active
// persona that uses the external agent. It is published as it is: nothing is planned,
// reviewed or run, and the persona's memory and schedules are not touched.
// Nobody is routed it; the user replying to it wakes the persona as usual.
// It returns the post and how many more the persona may make today.
func (r *Runner) PostAsPersona(ctx context.Context, personaID, text string) (timeline.Entry, int, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, `\n`, "\n"))
	if text == "" {
		return timeline.Entry{}, 0, errors.New("本文が空です")
	}
	if len([]rune(text)) > maxDirectReply {
		return timeline.Entry{}, 0, errors.New("本文が長すぎます")
	}
	skill, ok := r.catalogue.Find(ctx, personaID)
	if !ok {
		return timeline.Entry{}, 0, errors.New("担当が見つかりません")
	}
	_, settings := r.clientOf()
	// A persona switched off keeps its planner choice; it does not post.
	if !slices.Contains(settings.Active, skill.ID) || !settings.PlansExternally(skill.ID) {
		return timeline.Entry{}, 0, ErrNotAgentPersona
	}
	now := time.Now()
	day := now.In(settings.Location()).Format("2006-01-02")
	used, err := r.countAgentPost(ctx, day, skill.ID, 1)
	if err != nil {
		return timeline.Entry{}, 0, err
	}
	id := store.NewID("e")
	post := timeline.Entry{
		ID: id, Kind: timeline.KindPost, Speaker: skill.ID,
		Text: text, At: now, Root: id, Model: "agent",
	}
	if err := r.store.Append(post); err != nil {
		// Not posted: the day keeps its count.
		if _, undo := r.countAgentPost(context.WithoutCancel(ctx), day, skill.ID, -1); undo != nil {
			r.emit("error", map[string]any{"where": "agent-post", "message": undo.Error()})
		}
		return timeline.Entry{}, 0, err
	}
	r.emit("entry", post)
	return post, MaxAgentPostsPerDay - used, nil
}

// countAgentPost changes one persona's count of agent posts on a day by delta
// and returns the new count, refusing to go past the day's limit.
func (r *Runner) countAgentPost(ctx context.Context, day, personaID string, delta int) (int, error) {
	var count int
	// The record outlives its day only long enough for any time zone.
	expire := time.Now().Add(72 * time.Hour)
	err := r.store.UpdateRecord(ctx, store.RecordAgentPosts, day, expire, func(raw []byte, exists bool) ([]byte, error) {
		counts := map[string]int{}
		if exists {
			if err := json.Unmarshal(raw, &counts); err != nil {
				return nil, err
			}
		}
		count = max(counts[personaID]+delta, 0)
		if delta > 0 && count > MaxAgentPostsPerDay {
			return nil, ErrAgentPostsExhausted
		}
		counts[personaID] = count
		return json.Marshal(counts)
	})
	return count, err
}
