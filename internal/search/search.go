// Package search retrieves the same saved fragments for the UI and personas.
package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"golang.org/x/text/unicode/norm"
)

const Model = "Qwen/Qwen3-Embedding-8B"
const Dimensions = 1024

var ErrQuery = errors.New("検索語は1〜500文字で入力してください")
var ErrMode = errors.New("検索方法を選んでください")

// ErrBusy means another search (often a persona's) is still indexing. Waiting
// behind it would outlast the UI request, so the caller is told to come back.
var ErrBusy = errors.New("ほかの検索が意味検索を準備中で混んでいます。少し待ってからもう一度検索してください")

const gateWait = 5 * time.Second

type Hit struct {
	Entry   timeline.Entry `json:"entry"`
	Snippet string         `json:"snippet"`
	Score   float64        `json:"score"`
}

type Result struct {
	Hits  []Hit `json:"hits"`
	Total int   `json:"total"`
}

type vectorStore interface {
	SearchVector(context.Context, string) (store.SearchVector, error)
	SaveSearchVector(context.Context, string, store.SearchVector) error
}

type Service struct {
	workspace store.Workspace
	gate      chan struct{}
	gateWait  time.Duration
	vectors   map[string][]float64
}

func New(workspace store.Workspace) *Service {
	return &Service{workspace: workspace, gate: make(chan struct{}, 1), gateWait: gateWait, vectors: map[string][]float64{}}
}

// enter takes the indexing gate, giving up with ErrBusy rather than queueing
// behind a pass that may run for minutes. The caller must call leave.
func (s *Service) enter(ctx context.Context) error {
	timer := time.NewTimer(s.gateWait)
	defer timer.Stop()
	select {
	case s.gate <- struct{}{}:
		return nil
	case <-timer.C:
		return ErrBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) leave() { <-s.gate }

func Text(entry timeline.Entry) string {
	parts := []string{entry.Text, entry.Link}
	if entry.Media != nil {
		parts = append(parts, entry.Media.Text, entry.Media.Describe, entry.Media.Origin)
	}
	if entry.Place != nil {
		parts = append(parts, entry.Place.Label)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func normalize(text string) string { return strings.ToLower(norm.NFKC.String(text)) }

// Search uses the current user's timeline, including older, unpaged posts.
// Vectors are cached persistently. Interrupted indexing resumes on the next
// request because each successful batch is saved before the next is started.
func (s *Service) Search(ctx context.Context, query, mode string, limit, offset int) (Result, error) {
	query = strings.TrimSpace(query)
	if len([]rune(query)) < 1 || len([]rune(query)) > 500 {
		return Result{}, ErrQuery
	}
	if mode != "keyword" && mode != "semantic" {
		return Result{}, ErrMode
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	if mode == "semantic" {
		if index, ok := s.workspace.(nativeIndex); ok {
			return s.nativeSearch(ctx, index, query, limit, offset)
		}
	}
	entries, err := store.SearchTimeline(ctx, s.workspace)
	if err != nil {
		return Result{}, err
	}
	roots := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind == timeline.KindPost && !entry.Deleted {
			roots[entry.ID] = true
		}
	}
	var candidates []timeline.Entry
	for _, entry := range entries {
		if !entry.Deleted && roots[timeline.RootOf(entry)] && Text(entry) != "" {
			candidates = append(candidates, entry)
		}
	}
	hits := []Hit{}
	if mode == "keyword" {
		terms := strings.Fields(normalize(query))
		for _, entry := range candidates {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			text := Text(entry)
			match := true
			for _, term := range terms {
				if !strings.Contains(normalize(text), term) {
					match = false
					break
				}
			}
			if match {
				hits = append(hits, Hit{Entry: entry, Snippet: excerpt(text, terms[0]), Score: 1})
			}
		}
	} else if len(candidates) > 0 {
		// Share one indexing pass between concurrent UI searches and personas.
		if err := s.enter(ctx); err != nil {
			return Result{}, err
		}
		defer s.leave()
		hits, err = s.semantic(ctx, candidates, query)
		if err != nil {
			return Result{}, err
		}
		// Indexing can take time. Never return a post deleted or changed while
		// embeddings were being generated (including children of a deleted root).
		latest, err := store.SearchTimeline(ctx, s.workspace)
		if err != nil {
			return Result{}, err
		}
		current := map[string]timeline.Entry{}
		for _, entry := range latest {
			if !entry.Deleted {
				current[entry.ID] = entry
			}
		}
		filtered := hits[:0]
		for _, hit := range hits {
			entry, exists := current[hit.Entry.ID]
			root, rootExists := current[timeline.RootOf(hit.Entry)]
			if exists && rootExists && root.Kind == timeline.KindPost && Text(entry) == Text(hit.Entry) {
				filtered = append(filtered, hit)
			}
		}
		hits = filtered
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].Entry.At.Equal(hits[j].Entry.At) {
			return hits[i].Entry.At.After(hits[j].Entry.At)
		}
		return hits[i].Entry.ID > hits[j].Entry.ID
	})
	result := Result{Hits: []Hit{}, Total: len(hits)}
	if offset < len(hits) {
		result.Hits = hits[offset:min(len(hits), offset+limit)]
	}
	return result, nil
}

func excerpt(text, term string) string {
	runes := []rune(text)
	start := 0
	if term != "" {
		folded := normalize(text)
		if index := strings.Index(folded, term); index >= 0 {
			start = min(len(runes), max(0, len([]rune(folded[:index]))-50))
		}
	}
	end := min(len(runes), start+240)
	result := string(runes[start:end])
	if start > 0 {
		result = "…" + result
	}
	if end < len(runes) {
		result += "…"
	}
	return result
}

func chunks(text string) []string {
	runes := []rune(text)
	var out []string
	for start := 0; start < len(runes); start += 1800 {
		end := min(start+2000, len(runes))
		out = append(out, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return out
}

func (s *Service) semantic(ctx context.Context, entries []timeline.Entry, query string) ([]Hit, error) {
	settings := s.workspace.Settings()
	if settings.APIKey == "" {
		return nil, errors.New("設定でAIのAPIキーを入力してください")
	}
	cache, ok := s.workspace.(vectorStore)
	if !ok {
		return nil, errors.New("この保存先は意味検索に対応していません")
	}
	type passage struct {
		entry     int
		text, key string
	}
	var passages []passage
	vectors := map[string][]float64{}
	var missing []passage
	seen := map[string]bool{}
	for i, entry := range entries {
		for _, text := range chunks(Text(entry)) {
			key := fmt.Sprintf("%x", sha256.Sum256([]byte(settings.BaseURL+"\x00"+Model+"\x001024\x00"+text)))
			p := passage{i, text, key}
			passages = append(passages, p)
			if seen[key] {
				continue
			}
			seen[key] = true
			if vector, ok := s.vectors[key]; ok {
				vectors[key] = vector
				continue
			}
			cached, err := cache.SearchVector(ctx, key)
			if err != nil {
				return nil, err
			}
			if validVector(cached.Vector) {
				vectors[key] = cached.Vector
				s.remember(key, cached.Vector)
			} else {
				missing = append(missing, p)
			}
		}
	}
	for start := 0; start < len(missing); start += 16 {
		batch := missing[start:min(start+16, len(missing))]
		texts := make([]string, len(batch))
		for i, p := range batch {
			texts[i] = p.text
		}
		generated, err := embed(ctx, settings, texts)
		if err != nil {
			return nil, err
		}
		for i, p := range batch {
			if err := cache.SaveSearchVector(ctx, p.key, store.SearchVector{Vector: generated[i]}); err != nil {
				return nil, err
			}
			vectors[p.key] = generated[i]
			s.remember(p.key, generated[i])
		}
	}
	queryVectors, err := embed(ctx, settings, []string{"Instruct: Retrieve saved personal posts relevant to the search query.\nQuery: " + query})
	if err != nil {
		return nil, err
	}
	best := map[int]Hit{}
	for _, p := range passages {
		score := cosine(queryVectors[0], vectors[p.key])
		if old, ok := best[p.entry]; !ok || score > old.Score {
			best[p.entry] = Hit{Entry: entries[p.entry], Snippet: excerpt(p.text, ""), Score: score}
		}
	}
	hits := make([]Hit, 0, len(best))
	for _, hit := range best {
		hits = append(hits, hit)
	}
	return hits, nil
}

// Access is serialized by gate; cap resident vectors for long-lived servers.
func (s *Service) remember(key string, vector []float64) {
	if len(s.vectors) >= 4096 {
		clear(s.vectors)
	}
	s.vectors[key] = vector
}

func validVector(vector []float64) bool {
	if len(vector) != Dimensions {
		return false
	}
	var length float64
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
		length += value * value
	}
	return length > 0 && !math.IsInf(length, 0)
}

func cosine(a, b []float64) float64 {
	var dot, aa, bb float64
	for i, value := range a {
		dot += value * b[i]
		aa += value * value
		bb += b[i] * b[i]
	}
	return dot / math.Sqrt(aa*bb)
}

func embed(ctx context.Context, settings store.Settings, texts []string) ([][]float64, error) {
	body, _ := json.Marshal(map[string]any{"model": Model, "input": texts, "dimensions": Dimensions, "encoding_format": "float"})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(settings.BaseURL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+settings.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("意味検索に接続できませんでした。もう一度検索してください")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("意味検索のモデル呼び出しに失敗しました (%d)。接続先とAPIキーを確認してください", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, errors.New("意味検索の応答を読めませんでした")
	}
	if len(payload.Data) != len(texts) {
		return nil, errors.New("意味検索のベクトル数が不正です")
	}
	result := make([][]float64, len(texts))
	for _, row := range payload.Data {
		if row.Index < 0 || row.Index >= len(result) || result[row.Index] != nil || !validVector(row.Embedding) {
			return nil, errors.New("意味検索のベクトルが不正です")
		}
		result[row.Index] = row.Embedding
	}
	return result, nil
}
