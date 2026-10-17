package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"net/http"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/social"
	"github.com/takeshy/kakeratta/internal/store"
)

type socialBody struct {
	Root       string                       `json:"root"`
	Target     string                       `json:"target"`
	EntryIDs   []string                     `json:"entryIds"`
	Digest     string                       `json:"digest"`
	HTMLImages map[string]social.HTMLImages `json:"htmlImages,omitempty"`
	Formats    map[string]string            `json:"formats,omitempty"`
	// Language is the display language the words the plan adds use.
	Language string `json:"language,omitempty"`
}

func (s *Server) socialPlan(r *http.Request, body socialBody) (social.Plan, error) {
	entries, err := store.SearchThread(r.Context(), s.store, body.Root)
	if err != nil {
		return social.Plan{}, err
	}
	skills := s.catalogue.Snapshot(r.Context())
	plan, err := social.PlanSelectionFormatsIn(body.Language, body.Target, body.Root, entries, body.EntryIDs, func(id string) string {
		if p, ok := skills.Find(id); ok {
			return strings.TrimSpace(p.Icon + " " + p.Name)
		}
		return "◎ " + id
	}, body.HTMLImages, body.Formats)
	if err != nil {
		return plan, err
	}
	_, persistence, err := s.blueskyOAuth()
	if err != nil {
		return plan, err
	}
	connection, err := persistence.Connection(r.Context())
	if err != nil {
		return plan, err
	}
	plan.Account = connection.DID.String()
	plan.Handle = connection.Handle
	plan.Seal()
	return plan, nil
}
func (s *Server) previewSocial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body socialBody
	if readJSONLimit(r, &body, 32<<20) != nil {
		fail(w, 400, "選択内容を読めませんでした")
		return
	}
	plan, err := s.socialPlan(r, body)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	type previewPost struct {
		social.Post
		Publication store.Publication `json:"publication"`
	}
	posts := make([]previewPost, 0, len(plan.Posts))
	remaining := plan
	remaining.Posts = nil
	for _, post := range plan.Posts {
		publication, err := s.store.Publication(plan.Target + ":" + plan.Account + ":" + post.Key())
		if err != nil {
			fail(w, 503, "投稿履歴を読み込めませんでした")
			return
		}
		posts = append(posts, previewPost{post, publication})
		if publication.State != "posted" {
			remaining.Posts = append(remaining.Posts, post)
		}
	}
	if _, err := s.prepareSocialMedia(remaining); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, struct {
		social.Plan
		Posts []previewPost `json:"posts"`
	}{plan, posts})
}

type socialImage struct {
	data []byte
	mime string
}

func (s *Server) prepareSocialMedia(plan social.Plan) (map[string]socialImage, error) {
	images := make(map[string]socialImage)
	for _, p := range plan.Posts {
		if p.Image != "" {
			data, mime, err := decodeImage(p.Image)
			if err != nil || (mime != "image/png" && mime != "image/jpeg") {
				return nil, fmt.Errorf("HTMLの画像を読み込めませんでした")
			}
			config, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 50_000_000 {
				return nil, fmt.Errorf("HTMLの画像形式または解像度が不正です")
			}
			data, mime, err = social.PrepareImage(data, mime)
			if err != nil {
				return nil, err
			}
			images[p.Key()] = socialImage{data, mime}
			continue
		}
		if p.BlobID == "" {
			continue
		}
		if _, ok := images[p.BlobID]; ok {
			continue
		}
		data, mime, err := s.store.Blob(p.BlobID)
		if err != nil {
			return nil, fmt.Errorf("画像を読み込めませんでした")
		}
		data, mime, err = social.PrepareImage(data, mime)
		if err != nil {
			return nil, err
		}
		images[p.BlobID] = socialImage{data, mime}
	}
	return images, nil
}
func (s *Server) publishSocial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body socialBody
	if readJSONLimit(r, &body, 32<<20) != nil {
		fail(w, 400, "投稿内容を読めませんでした")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	app, persistence, err := s.blueskyOAuth()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	release, err := persistence.Acquire(ctx)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	defer release()
	plan, err := s.socialPlan(r, body)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if body.Digest == "" || body.Digest != plan.Digest {
		fail(w, 409, "内容が変わりました。もう一度プレビューしてください")
		return
	}
	images, err := s.prepareSocialMedia(plan)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	connection, err := persistence.Connection(ctx)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	var client *social.Client
	if s.socialClient != nil {
		client, err = s.socialClient(ctx)
	} else {
		session, resumeErr := app.ResumeSession(ctx, connection.DID, connection.SessionID)
		if resumeErr != nil {
			fail(w, 401, "Blueskyに再接続してください")
			return
		}
		client = social.NewClient(session.APIClient())
	}
	if err != nil || client == nil {
		fail(w, 401, "Blueskyに再接続してください")
		return
	}
	account := client.Account()
	if account == "" || account != plan.Account {
		fail(w, 409, "接続アカウントが変わりました。もう一度プレビューしてください")
		return
	}
	refs := map[string]store.Publication{}
	type published struct {
		EntryID     string            `json:"entryId"`
		Publication store.Publication `json:"publication"`
	}
	results := []published{}
	respond := func(message string) { writeJSON(w, 200, map[string]any{"posts": results, "error": message}) }
	for _, post := range plan.Posts {
		key := body.Target + ":" + account + ":" + post.Key()
		existing, err := s.store.Publication(key)
		if err != nil {
			respond("投稿履歴を読み込めませんでした")
			return
		}
		if existing.State == "posted" {
			// Older history predates answer-only sharing and always used the
			// original post as its Bluesky thread root.
			if existing.RootURI == "" {
				legacyRoot, err := s.store.Publication(body.Target + ":" + account + ":" + body.Root)
				if err != nil || legacyRoot.State != "posted" {
					respond("投稿スレッドの履歴を読み込めませんでした")
					return
				}
				existing.RootURI, existing.RootCID = legacyRoot.URI, legacyRoot.CID
			}
			refs[post.Key()] = existing
			results = append(results, published{post.EntryID, existing})
			continue
		}
		if existing.State != "" && existing.State != "retryable" {
			respond("投稿処理中、または前回の結果が未確認です。SNS上の投稿を確認してください。重複防止のため再送していません。")
			return
		}
		attachment := images[post.BlobID]
		if post.Image != "" {
			attachment = images[post.Key()]
		}
		claimed, err := s.store.ClaimPublication(key, store.Publication{State: "pending"})
		if err != nil || !claimed {
			respond("別の投稿処理が進行中です。しばらく待ってから再確認してください。")
			return
		}
		parent := refs[post.Parent]
		root := store.Publication{URI: parent.RootURI, CID: parent.RootCID}
		ref, err := client.Publish(ctx, post, root, parent, attachment.data, attachment.mime)
		if err != nil {
			if ref.State == "retryable" {
				if s.store.SavePublication(key, ref) == nil {
					respond(err.Error() + "。成功分は保存済みです。問題を解消してから再操作すると未投稿分を送れます。")
					return
				}
			}
			respond(err.Error() + "。ここまでの成功分は保存済みです。この発言はSNS上で結果を確認してください。")
			return
		}
		if err := s.store.SavePublication(key, ref); err != nil {
			respond("SNSへの投稿は成功しましたが履歴を保存できませんでした。SNS上で確認してください。")
			return
		}
		refs[post.Key()] = ref
		results = append(results, published{post.EntryID, ref})
	}
	respond("")
}
