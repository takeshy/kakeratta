package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Agent tasks are a bounded durable inbox. CAS uses a Firestore transaction in
// hosted workspaces and the store lock locally. Claims never expire implicitly:
// reconnecting must not automatically repeat an external side effect.
type AgentTask struct {
	ID          string     `json:"id"`
	Trigger     string     `json:"trigger,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	Kind        string     `json:"kind"`
	Prompt      string     `json:"prompt,omitempty"`
	At          time.Time  `json:"at"`
	State       string     `json:"state"`
	Claim       string     `json:"claim,omitempty"`
	ClaimedAt   *time.Time `json:"claimedAt"`
	Result      string     `json:"result,omitempty"`
	Published   bool       `json:"published,omitempty"`
}

const taskQueueKey = "mcp-hermes-tasks"

func CancelAgentTask(ctx context.Context, s Workspace, id string) error {
	return changeAgentTasks(ctx, s, func(tasks []AgentTask) ([]AgentTask, error) {
		for i := range tasks {
			if tasks[i].ID == id && tasks[i].State != "completed" {
				tasks[i].State = "completed"
				tasks[i].Published = true
			}
		}
		return tasks, nil
	})
}

func AgentTasks(ctx context.Context, s Workspace) ([]AgentTask, error) {
	v, err := s.SocialValue(ctx, taskQueueKey)
	if err != nil {
		return nil, err
	}
	var tasks []AgentTask
	if len(v.Data) > 0 {
		err = json.Unmarshal(v.Data, &tasks)
	}
	return tasks, err
}

func changeAgentTasks(ctx context.Context, s Workspace, change func([]AgentTask) ([]AgentTask, error)) error {
	for attempt := 0; attempt < 12; attempt++ {
		v, err := s.SocialValue(ctx, taskQueueKey)
		if err != nil {
			return err
		}
		var tasks []AgentTask
		if len(v.Data) > 0 {
			if err := json.Unmarshal(v.Data, &tasks); err != nil {
				return err
			}
		}
		tasks, err = change(tasks)
		if err != nil {
			return err
		}
		// Keep recent completed claims for retrying lost replies. Unpublished
		// results are retained until timeline delivery succeeds.
		completed := 0
		for i := len(tasks) - 1; i >= 0; i-- {
			if tasks[i].State == "completed" && tasks[i].Published {
				completed++
				if completed > 8 {
					tasks = append(tasks[:i], tasks[i+1:]...)
				}
			}
		}
		raw, err := json.Marshal(tasks)
		if err != nil {
			return err
		}
		if len(raw) > 800<<10 {
			return errors.New("Hermesの依頼ボックスがいっぱいです")
		}
		ok, err := s.SwapSocialValue(ctx, taskQueueKey, v.Version, SocialValue{Data: raw})
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return errors.New("依頼が更新されています。再試行してください")
}

func EnqueueAgentTask(ctx context.Context, s Workspace, task AgentTask) error {
	if len(task.Prompt) > 4096 {
		return errors.New("依頼の説明が長すぎます")
	}
	return changeAgentTasks(ctx, s, func(tasks []AgentTask) ([]AgentTask, error) {
		pending := 0
		for _, old := range tasks {
			if old.ID == task.ID {
				return tasks, nil
			}
			if old.State != "completed" {
				pending++
			}
		}
		if pending >= 128 {
			return nil, errors.New("Hermesの未処理の依頼が128件あります")
		}
		task.State = "pending"
		return append(tasks, task), nil
	})
}

func ClaimAgentTask(ctx context.Context, s Workspace, id, claim string) (AgentTask, error) {
	var found AgentTask
	err := changeAgentTasks(ctx, s, func(tasks []AgentTask) ([]AgentTask, error) {
		for i := range tasks {
			if tasks[i].ID != id {
				continue
			}
			t := &tasks[i]
			if t.State != "pending" && t.Claim != claim {
				return nil, errors.New("この依頼は既に取得されています")
			}
			if t.State == "pending" {
				t.State = "claimed"
				t.Claim = claim
				now := time.Now().UTC()
				t.ClaimedAt = &now
			}
			found = *t
			return tasks, nil
		}
		return nil, errors.New("依頼が見つかりません")
	})
	return found, err
}

// Commit the result before timeline delivery. A retry resumes delivery, never
// the agent's work, and cannot change the already accepted result.
func FinishAgentTask(ctx context.Context, s Workspace, id, claim, result string) (AgentTask, error) {
	var found AgentTask
	err := changeAgentTasks(ctx, s, func(tasks []AgentTask) ([]AgentTask, error) {
		for i := range tasks {
			if tasks[i].ID != id {
				continue
			}
			t := &tasks[i]
			if t.Claim != claim || t.State == "pending" {
				return nil, errors.New("依頼の取得IDが一致しません")
			}
			if t.State != "completed" {
				t.State = "completed"
				t.Result = result
			}
			found = *t
			return tasks, nil
		}
		return nil, errors.New("依頼が見つかりません")
	})
	return found, err
}

func MarkAgentTaskPublished(ctx context.Context, s Workspace, id string) error {
	return changeAgentTasks(ctx, s, func(tasks []AgentTask) ([]AgentTask, error) {
		for i := range tasks {
			if tasks[i].ID == id && tasks[i].State == "completed" {
				tasks[i].Published = true
			}
		}
		return tasks, nil
	})
}
