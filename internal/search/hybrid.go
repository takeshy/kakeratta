package search

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Unified combines ranks, never incomparable lexical and cosine scores.
// The semantic budget leaves time to return lexical matches if the provider
// is unavailable or indexing is still in progress. Completed indexing persists.
func (s *Service) Unified(ctx context.Context, query string, limit, offset int, minScore float64) (Result, error) {
	query = strings.TrimSpace(query)
	if n := len([]rune(query)); n < 1 || n > 500 {
		return Result{}, ErrQuery
	}
	if math.IsNaN(minScore) || math.IsInf(minScore, 0) || minScore < 0 || minScore > 1 {
		return Result{}, ErrScore
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	semanticCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	semantic, semanticErr := s.Search(semanticCtx, query, "semantic", 100, 0)
	cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	// Read after the potentially slow embedding call, so deleted/changed
	// sources cannot be resurrected through the lexical branch or fusion.
	entries, err := store.SearchTimeline(ctx, s.workspace)
	if err != nil {
		return Result{}, err
	}
	current := map[string]timeline.Entry{}
	for _, e := range entries {
		if !e.Deleted {
			current[e.ID] = e
		}
	}
	var candidates []timeline.Entry
	for _, e := range current {
		if root, ok := current[timeline.RootOf(e)]; ok && root.Kind == timeline.KindPost && Text(e) != "" {
			candidates = append(candidates, e)
		}
	}
	lexical := lexicalHits(candidates, query)
	var eligible []Hit
	if semanticErr == nil {
		for _, h := range semantic.Hits {
			e, ok := current[h.Entry.ID]
			root, rootOK := current[timeline.RootOf(h.Entry)]
			if ok && rootOK && root.Kind == timeline.KindPost && Text(e) == Text(h.Entry) && h.Score >= minScore && !math.IsNaN(h.Score) && !math.IsInf(h.Score, 0) {
				h.Entry = e
				eligible = append(eligible, h)
			}
		}
	}
	hits := fuse(lexical, eligible)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result := Result{Hits: []Hit{}, Total: len(hits), SemanticUnavailable: semanticErr != nil}
	if offset < len(hits) {
		result.Hits = hits[offset:min(len(hits), offset+limit)]
	}
	return result, nil
}

// Script runs preserve names such as ピノ and ASCII model numbers without
// requiring an external tokenizer. Han bigrams also find compound substrings.
// This is deliberately lightweight, not a Japanese morphological analyzer.
func queryTerms(query string) []string {
	class := func(r rune) int {
		switch {
		case unicode.Is(unicode.Han, r):
			return 1
		case unicode.Is(unicode.Katakana, r) || r == 'ー':
			return 2
		case unicode.Is(unicode.Hiragana, r):
			return 3
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return 4
		default:
			return 0
		}
	}
	stop := map[string]bool{}
	for _, word := range strings.Fields("の は が を に で と も へ や 前 今 私 自分 以前 記録 投稿 検索 これ それ あれ こと もの です でした ます ました だった して した する んだ かった くて について 教えて 探して the a an of to in on for and was is it i my that this") {
		stop[word] = true
	}
	seen := map[string]bool{}
	var out []string
	add := func(word string) {
		word = normalize(word)
		if word != "" && !stop[word] && !seen[word] {
			seen[word] = true
			out = append(out, word)
		}
	}
	// Preserve an exact name even when a short hiragana run (e.g. すし)
	// would otherwise be discarded as a likely grammatical particle.
	if whole := normalize(strings.TrimSpace(query)); !strings.ContainsAny(whole, " \t\n\r") {
		add(whole)
	}
	var run []rune
	kind := 0
	flush := func() {
		if len(run) == 0 {
			return
		}
		word := string(run)
		if !stop[word] {
			if kind != 3 || len(run) >= 3 {
				add(word)
			}
			if kind == 1 && len(run) > 2 {
				for i := 0; i+1 < len(run); i++ {
					add(string(run[i : i+2]))
				}
			}
		}
		run = nil
	}
	for _, r := range normalizeWidth(query) {
		next := class(r)
		if next != kind || next == 0 {
			flush()
		}
		kind = next
		if next != 0 {
			run = append(run, r)
		}
	}
	flush()
	return out
}

func lexicalHits(entries []timeline.Entry, query string) []Hit {
	terms := queryTerms(query)
	weights := map[string]float64{}
	texts := make([]string, len(entries))
	for i, e := range entries {
		texts[i] = normalize(Text(e))
	}
	for _, term := range terms {
		df := 0
		for _, text := range texts {
			if strings.Contains(text, term) {
				df++
			}
		}
		weight := math.Log(1 + float64(len(entries)+1)/float64(df+1))
		if len([]rune(term)) == 1 {
			weight *= 0.25
		}
		weights[term] = weight
	}
	var hits []Hit
	for i, e := range entries {
		score := 0.0
		for _, term := range terms {
			if strings.Contains(texts[i], term) {
				score += weights[term]
			}
		}
		if score == 0 {
			continue
		}
		bestScore := -1.0
		passage, anchor := "", ""
		for _, part := range chunks(Text(e)) {
			folded, partScore, strongest := normalize(part), 0.0, 0.0
			termAt := ""
			for _, term := range terms {
				if strings.Contains(folded, term) {
					partScore += weights[term]
					if weights[term] > strongest {
						strongest, termAt = weights[term], term
					}
				}
			}
			if partScore > bestScore {
				bestScore, passage, anchor = partScore, part, termAt
			}
		}
		hits = append(hits, Hit{Entry: e, Score: score, Snippet: excerpt(passage, anchor), Passage: passage})
	}
	sortHits(hits)
	return hits
}

func sortHits(hits []Hit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].Entry.At.Equal(hits[j].Entry.At) {
			return hits[i].Entry.At.After(hits[j].Entry.At)
		}
		return hits[i].Entry.ID > hits[j].Entry.ID
	})
}

func fuse(lexical, semantic []Hit) []Hit {
	best := map[string]Hit{}
	for _, list := range [][]Hit{lexical, semantic} {
		for rank, h := range list {
			contribution := 1 / float64(60+rank+1)
			if previous, ok := best[h.Entry.ID]; ok {
				previous.Score += contribution
				// Keep both matching passages when the two routes found different
				// evidence. Their existing chunk bounds cap persona context.
				if h.Passage != "" && !strings.Contains(previous.Passage, h.Passage) {
					previous.Passage += "\n…\n" + h.Passage
				}
				best[h.Entry.ID] = previous
			} else {
				h.Score = contribution
				best[h.Entry.ID] = h
			}
		}
	}
	hits := make([]Hit, 0, len(best))
	for _, h := range best {
		hits = append(hits, h)
	}
	sortHits(hits)
	return hits
}
