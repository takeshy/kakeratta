// Package social publishes explicitly selected conversation entries.
package social

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/takeshy/kakeratta/internal/timeline"
)

type Post struct {
	ID      string `json:"id,omitempty"`
	EntryID string `json:"entryId"`
	Parent  string `json:"parent,omitempty"`
	Text    string `json:"text"`
	BlobID  string `json:"blobId,omitempty"`
}

func (p Post) Key() string {
	if p.ID != "" {
		return p.ID
	}
	return p.EntryID
}

type Plan struct {
	Account string `json:"account"`
	Handle  string `json:"handle"`
	Target  string `json:"target"`
	Root    string `json:"root"`
	Posts   []Post `json:"posts"`
	Digest  string `json:"digest"`
}

// PlanSelection requires an unbroken ancestry; it never quietly includes a
// private parent or reattaches a reply to a different statement.
func PlanSelection(target, root string, entries []timeline.Entry, selected []string, label func(string) string) (Plan, error) {
	p := Plan{Target: target, Root: root}
	if target != "bluesky" {
		return p, fmt.Errorf("投稿先はBlueskyを選んでください")
	}
	if len(selected) == 0 || len(selected) > 100 {
		return p, fmt.Errorf("1〜100件の発言を選んでください")
	}
	all := map[string]timeline.Entry{}
	for _, e := range entries {
		if !e.Deleted && (e.ID == root || e.Root == root) {
			all[e.ID] = e
		}
	}
	if e, ok := all[root]; !ok || e.Kind != timeline.KindPost {
		return p, fmt.Errorf("元の投稿が見つかりません")
	}
	chosen := map[string]bool{}
	for _, id := range selected {
		if _, ok := all[id]; !ok {
			return p, fmt.Errorf("選択した発言が見つかりません")
		}
		chosen[id] = true
	}
	if !chosen[root] {
		return p, fmt.Errorf("元の投稿を選択してください")
	}
	tails := map[string]string{}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if done[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("返信の親子関係が不正です")
		}
		visiting[id] = true
		e := all[id]
		parent := ""
		if id != root {
			parent = e.ReplyTo
			if parent == "" {
				parent = root
			}
			if !chosen[parent] {
				return fmt.Errorf("返信先の発言も選択してください: %s", parent)
			}
			if err := visit(parent); err != nil {
				return err
			}
		}
		text := e.Text
		prefix := ""
		if e.Speaker != timeline.User {
			prefix = label(e.Speaker) + "からの返信:\n"
		}
		if e.Link != "" {
			text += "\n" + e.Link
		}
		for _, source := range e.Sources {
			if source.URL != "" {
				text += "\n" + source.URL
			}
		}
		parts, err := SplitText(strings.TrimSpace(text), prefix)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if parent != "" {
			parent = tails[parent]
		}
		for i, part := range parts {
			post := Post{EntryID: id, Parent: parent, Text: part}
			if i > 0 {
				post.ID = fmt.Sprintf("%s:part:%d", id, i+1)
			}
			if i == 0 && e.Media != nil {
				post.BlobID = e.Media.BlobID
			}
			if post.Text == "" && post.BlobID == "" {
				return fmt.Errorf("空の発言は投稿できません")
			}
			p.Posts = append(p.Posts, post)
			parent = post.Key()
		}
		tails[id] = parent
		done[id] = true
		return nil
	}
	// Timeline order gives siblings stable order regardless of checkbox order.
	for _, e := range entries {
		if chosen[e.ID] {
			if err := visit(e.ID); err != nil {
				return p, err
			}
		}
	}
	p.Seal()
	return p, nil
}

func (p *Plan) Seal() {
	p.Digest = ""
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	p.Digest = hex.EncodeToString(sum[:])
}
