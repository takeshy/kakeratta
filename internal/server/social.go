package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/social"
	"github.com/takeshy/kakeratta/internal/store"
)

type socialBody struct {
	Root     string   `json:"root"`
	Target   string   `json:"target"`
	EntryIDs []string `json:"entryIds"`
	Digest   string   `json:"digest"`
}

func (s *Server) socialPlan(r *http.Request, body socialBody) (social.Plan, error) {
	entries, err := store.SearchThread(r.Context(), s.store, body.Root)
	if err != nil {
		return social.Plan{}, err
	}
	plan, err := social.PlanSelection(body.Target, body.Root, entries, body.EntryIDs, func(id string) string {
		if p, ok := s.catalogue.Find(id); ok {
			return strings.TrimSpace(p.Icon + " " + p.Name)
		}
		return "◎ " + id
	})
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
	if readJSONLimit(r, &body, 64<<10) != nil {
		fail(w, 400, "選択内容を読めませんでした")
		return
	}
	plan, err := s.socialPlan(r, body)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := s.validateSocialMedia(plan); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, plan)
}
func (s *Server) validateSocialMedia(plan social.Plan) error {
	for _, p := range plan.Posts {
		if p.BlobID != "" {
			data, mime, err := s.store.Blob(p.BlobID)
			if err != nil {
				return fmt.Errorf("画像を読み込めませんでした")
			}
			if !strings.HasPrefix(mime, "image/") {
				return fmt.Errorf("画像形式が不正です")
			}
			if plan.Target == "bluesky" && len(data) > 1000000 {
				return fmt.Errorf("Blueskyの画像は1 MB以内にしてください")
			}
		}
	}
	return nil
}
func (s *Server) publishSocial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body socialBody
	if readJSONLimit(r, &body, 64<<10) != nil {
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
	if err = s.validateSocialMedia(plan); err != nil {
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
		key := body.Target + ":" + account + ":" + post.EntryID
		existing, err := s.store.Publication(key)
		if err != nil {
			respond("投稿履歴を読み込めませんでした")
			return
		}
		if existing.State == "posted" {
			refs[post.EntryID] = existing
			results = append(results, published{post.EntryID, existing})
			continue
		}
		if existing.State != "" && existing.State != "retryable" {
			respond("投稿処理中、または前回の結果が未確認です。SNS上の投稿を確認してください。重複防止のため再送していません。")
			return
		}
		var data []byte
		var mime string
		if post.BlobID != "" {
			data, mime, err = s.store.Blob(post.BlobID)
			if err != nil {
				respond("画像を読み込めませんでした")
				return
			}
		}
		claimed, err := s.store.ClaimPublication(key, store.Publication{State: "pending"})
		if err != nil || !claimed {
			respond("別の投稿処理が進行中です。しばらく待ってから再確認してください。")
			return
		}
		ref, err := client.Publish(ctx, post, refs[plan.Root], refs[post.Parent], data, mime)
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
		refs[post.EntryID] = ref
		results = append(results, published{post.EntryID, ref})
	}
	respond("")
}
