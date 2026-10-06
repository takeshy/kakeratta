// Package social publishes explicitly selected conversation entries.
package social

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/takeshy/kakeratta/internal/timeline"
)

type Post struct {
	ID         string `json:"id,omitempty"`
	EntryID    string `json:"entryId"`
	Parent     string `json:"parent,omitempty"`
	Text       string `json:"text"`
	BlobID     string `json:"blobId,omitempty"`
	Image      string `json:"image,omitempty"`
	ImageAlt   string `json:"imageAlt,omitempty"`
	SourceHash string `json:"sourceHash,omitempty"`
	Links      []Link `json:"links,omitempty"`
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

// PlanSelection includes only explicitly selected entries. If a reply's
// parent is omitted, its publication attaches to the first selected entry instead.
func PlanSelection(target, root string, entries []timeline.Entry, selected []string, label func(string) string) (Plan, error) {
	return PlanSelectionWithImages(target, root, entries, selected, label, nil)
}

func PlanSelectionWithImages(target, root string, entries []timeline.Entry, selected []string, label func(string) string, images map[string]HTMLImages) (Plan, error) {
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
			if chosen[parent] {
				if err := visit(parent); err != nil {
					return err
				}
			} else {
				parent = ""
				if len(p.Posts) > 0 {
					parent = p.Posts[0].EntryID
				}
			}
		}
		text := e.Text
		var links []Link
		var rendered HTMLImages
		image := ImageAnswer(e)
		if e.Speaker != timeline.User && !image {
			// A persona's Markdown answer is posted as the text it reads as,
			// its labelled links as link facets. HTML answers and laid-out
			// infographics become images.
			text, links = FromMarkdown(e.Text)
		}
		if image {
			var ok bool
			rendered, ok = images[id]
			if !ok || len(rendered.Images) == 0 || len(rendered.Images) > 10 {
				return fmt.Errorf("画像にする回答を変換してからプレビューしてください")
			}
			for _, page := range rendered.Images {
				if page == "" {
					return fmt.Errorf("回答の画像を読み込めませんでした")
				}
			}
			hash := sha256.Sum256([]byte(e.Text))
			if rendered.SourceHash != hex.EncodeToString(hash[:]) {
				return fmt.Errorf("回答の内容が変わりました。会話を開き直してプレビューしてください")
			}
			text = "［画像］"
		}
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
		// Link facets point into the converted text, which FromMarkdown has
		// already trimmed at the front; trimming the end cannot move them.
		body := strings.TrimSpace(text)
		if links != nil {
			body = strings.TrimRightFunc(text, unicode.IsSpace)
		}
		parts, err := SplitRich(body, prefix, links)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if parent != "" {
			parent = tails[parent]
		}
		for i, part := range parts {
			post := Post{EntryID: id, Parent: parent, Text: part.Text, Links: part.Links}
			if i > 0 {
				post.ID = fmt.Sprintf("%s:part:%d", id, i+1)
			}
			if i == 0 && e.Media != nil {
				post.BlobID = e.Media.BlobID
			}
			if i == 0 && len(rendered.Images) > 0 {
				post.BlobID = ""
				post.Image = rendered.Images[0]
				post.ImageAlt = rendered.Alt
				post.SourceHash = rendered.SourceHash
			}
			if post.Text == "" && post.BlobID == "" {
				return fmt.Errorf("空の発言は投稿できません")
			}
			p.Posts = append(p.Posts, post)
			parent = post.Key()
		}
		for i := 1; i < len(rendered.Images); i++ {
			post := Post{ID: fmt.Sprintf("%s:image:%d", id, i+1), EntryID: id, Parent: parent,
				Text:  continuation + prefix + fmt.Sprintf("［画像 %d/%d］", i+1, len(rendered.Images)),
				Image: rendered.Images[i], ImageAlt: rendered.Alt, SourceHash: rendered.SourceHash}
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
	p.Root = p.Posts[0].Key()
	p.Seal()
	return p, nil
}

func (p *Plan) Seal() {
	p.Digest = ""
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	p.Digest = hex.EncodeToString(sum[:])
}
