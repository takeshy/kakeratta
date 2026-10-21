package persona

import (
	"context"
	"testing"
	"time"
)

// A suggested persona starts with the facts and records picked for it, word
// for word, and the persona that suggested it keeps its own.
func TestSeedMemoryCopiesWhatWasPicked(t *testing.T) {
	r, s, _ := setup(t, answering, "gourmet")
	settings := s.Settings()
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	source := memo{
		Facts:   []fact{{ID: "f_nuka", Text: "ぬか床を毎朝かき混ぜている", From: []string{"e_a"}}, {ID: "f_ramen", Text: "豚骨ラーメンが好き", From: []string{"e_b"}}},
		Records: []record{{At: at, ID: "e_a", Summary: "ぬか床を仕込んだ", Tags: []string{"発酵"}, Asked: "e_q"}, {At: at, ID: "e_b", Summary: "二郎に行った"}},
	}
	if err := s.WriteNote("gourmet", MemoPath, memoText(source, time.UTC)); err != nil {
		t.Fatal(err)
	}
	facts, records, err := r.SeedMemory(context.Background(), "gourmet", "ferment", []string{"f_nuka", "f_made_up"}, []string{"e_a"})
	if err != nil || facts != 1 || records != 1 {
		t.Fatalf("seeded %d facts, %d records: %v", facts, records, err)
	}
	got := r.loadMemo("ferment", time.UTC)
	if len(got.Facts) != 1 || got.Facts[0].Text != "ぬか床を毎朝かき混ぜている" || len(got.Facts[0].From) != 1 ||
		len(got.Records) != 1 || got.Records[0].Summary != "ぬか床を仕込んだ" || got.Records[0].Asked != "" {
		t.Fatalf("new memory = %+v", got)
	}
	if kept := r.loadMemo("gourmet", time.UTC); len(kept.Facts) != 2 || len(kept.Records) != 2 {
		t.Fatalf("source memory = %+v", kept)
	}
}
