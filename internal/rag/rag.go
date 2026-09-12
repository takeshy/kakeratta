// Package rag finds the passage a reader half-remembers - "the bit where the
// key turns up" - and never returns one they have not reached.
//
// The index is lexical and local: no embedding service, no network, and it
// works the moment a book is imported, before any model is configured.
package rag

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/position"
)

const (
	passageRunes = 420
	overlapRunes = 90
)

// Passage is a searchable stretch of one section, with the positions it covers
// so a hit can be turned into "take me there".
type Passage struct {
	ID        string         `json:"id"`
	SectionID string         `json:"sectionId"`
	Title     string         `json:"title"`
	Range     position.Range `json:"range"`
	Text      string         `json:"text"`
}

// Index is the whole searchable book. Document frequencies are stored so a
// rebuilt index and a loaded one score identically.
type Index struct {
	BookID       string         `json:"bookId"`
	Passages     []Passage      `json:"passages"`
	DocFrequency map[string]int `json:"docFrequency"`
}

// Hit is a passage the query matched, cut off at the reader's boundary.
type Hit struct {
	Passage Passage  `json:"passage"`
	Score   float64  `json:"score"`
	Snippet string   `json:"snippet"`
	Terms   []string `json:"terms"`
}

// Build splits every section into overlapping passages. Sections are never
// crossed: a passage that spans a chapter break would return a position that
// reads as the wrong scene.
func Build(b *book.Book) *Index {
	index := &Index{BookID: b.ID, DocFrequency: map[string]int{}}
	for si := range b.Sections {
		section := &b.Sections[si]
		var runes []rune
		start := position.Zero
		flush := func(end position.Position) {
			text := strings.TrimSpace(string(runes))
			if text == "" {
				runes = nil
				return
			}
			index.Passages = append(index.Passages, Passage{
				ID:        fmt.Sprintf("%s-p%d", section.ID, len(index.Passages)+1),
				SectionID: section.ID,
				Title:     section.Title,
				Range:     position.Range{Start: start, End: end},
				Text:      text,
			})
			runes = nil
		}
		for bi := range section.Blocks {
			block := &section.Blocks[bi]
			if block.Plain == "" {
				continue
			}
			if len(runes) == 0 {
				start = block.Range.Start
			}
			runes = append(runes, []rune(block.Plain)...)
			runes = append(runes, '\n')
			if len(runes) >= passageRunes {
				flush(block.Range.End)
				// Overlap so a sentence split across the cut is still findable.
				if bi+1 < len(section.Blocks) {
					tail := []rune(block.Plain)
					if len(tail) > overlapRunes {
						tail = tail[len(tail)-overlapRunes:]
					}
					runes = append([]rune{}, tail...)
					start = block.Range.End
					start.Absolute -= len(tail)
					start.CharOffset -= len(tail)
					if start.CharOffset < 0 {
						start.CharOffset = 0
					}
				}
			}
		}
		if len(runes) > 0 {
			flush(section.Range.End)
		}
	}
	for _, passage := range index.Passages {
		for term := range termSet(passage.Text) {
			index.DocFrequency[term]++
		}
	}
	return index
}

// Search returns the passages that best match the query, never looking past the
// boundary. A passage the reader is standing in the middle of is returned cut
// at their position rather than dropped, so a scene just reached is findable.
func (i *Index) Search(query string, boundary position.Position, limit int) []Hit {
	terms := termCounts(query)
	if len(terms) == 0 || len(i.Passages) == 0 {
		return nil
	}
	total := float64(len(i.Passages))
	var hits []Hit
	for _, passage := range i.Passages {
		if passage.Range.Start.After(boundary) {
			continue
		}
		text := passage.Text
		if passage.Range.End.After(boundary) {
			visible := boundary.Absolute - passage.Range.Start.Absolute
			runes := []rune(text)
			if visible <= 0 {
				continue
			}
			if visible < len(runes) {
				text = string(runes[:visible])
			}
		}
		counts := termCounts(text)
		if len(counts) == 0 {
			continue
		}
		var score, norm float64
		var matched []string
		for term, weight := range counts {
			idf := math.Log(1 + total/float64(1+i.DocFrequency[term]))
			norm += float64(weight) * idf * float64(weight) * idf
		}
		norm = math.Sqrt(norm)
		if norm == 0 {
			continue
		}
		for term, queryWeight := range terms {
			count, ok := counts[term]
			if !ok {
				continue
			}
			idf := math.Log(1 + total/float64(1+i.DocFrequency[term]))
			score += float64(queryWeight) * idf * float64(count) * idf
			if len([]rune(term)) > 1 {
				matched = append(matched, term)
			}
		}
		if score <= 0 {
			continue
		}
		hits = append(hits, Hit{
			Passage: Passage{
				ID: passage.ID, SectionID: passage.SectionID, Title: passage.Title,
				Range: position.Range{Start: passage.Range.Start, End: passage.Range.End},
				Text:  text,
			},
			Score:   score / norm,
			Snippet: snippet(text, matched),
			Terms:   matched,
		})
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].Score > hits[b].Score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// termCounts tokenises for both Japanese and space-separated languages:
// character bigrams for scripts written without spaces, words elsewhere.
func termCounts(text string) map[string]int {
	counts := map[string]int{}
	var run []rune
	flushWord := func() {
		if len(run) > 0 {
			counts[strings.ToLower(string(run))]++
			run = nil
		}
	}
	var cjk []rune
	flushCJK := func() {
		for i := 0; i < len(cjk); i++ {
			counts[string(cjk[i])]++
			if i+1 < len(cjk) {
				counts[string(cjk[i:i+2])]++
			}
		}
		cjk = nil
	}
	for _, r := range text {
		switch {
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			run = append(run, r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return counts
}

func termSet(text string) map[string]bool {
	set := map[string]bool{}
	for term := range termCounts(text) {
		set[term] = true
	}
	return set
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) || r == '々' || r == 'ー'
}

// snippet centres the returned text on the strongest match, so the list reads
// like the scene rather than like the start of a page.
func snippet(text string, terms []string) string {
	runes := []rune(text)
	if len(runes) <= 160 {
		return text
	}
	best := 0
	for _, term := range terms {
		if index := strings.Index(text, term); index >= 0 {
			best = len([]rune(text[:index]))
			break
		}
	}
	start := best - 60
	if start < 0 {
		start = 0
	}
	end := start + 160
	if end > len(runes) {
		end = len(runes)
		start = end - 160
	}
	prefix, suffix := "…", "…"
	if start == 0 {
		prefix = ""
	}
	if end == len(runes) {
		suffix = ""
	}
	return prefix + string(runes[start:end]) + suffix
}
