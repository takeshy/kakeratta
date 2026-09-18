package persona

import (
	"reflect"
	"sort"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
)

// A note cites the post its turn is about, and the posts behind a note it
// rewrites, links to, or replaces. Reading a note for context passes nothing
// on, and the index and the log carry no post links at all.
func TestNotesInheritLinksOnlyFromWhatTheyBuildOn(t *testing.T) {
	post := store.NoteSource{EntryID: "p1", Label: "ラベルの写真"}
	prov := newProvenance(post)
	prov.noteRead("tastes/larrivet.md", "type: Feedback\n\n<!-- kakeratta:note-sources -->\n* [前の投稿](/?entry=old1)\n<!-- /kakeratta:note-sources -->\n")
	prov.noteRead("wines/altura.md", "type: Wine\n\n<!-- kakeratta:note-sources -->\n* [その投稿](/?entry=w1)\n<!-- /kakeratta:note-sources -->\n")
	ids := func(sources []store.NoteSource) []string {
		var out []string
		for _, source := range sources {
			out = append(out, source.EntryID)
		}
		sort.Strings(out)
		return out
	}
	if got := ids(prov.forWrite("wines/new.md", "type: Wine\n\n新しい白。")); !reflect.DeepEqual(got, []string{"p1"}) {
		t.Fatalf("a fresh note cites %v, want only the post", got)
	}
	if got := ids(prov.forWrite("wines/altura.md", "type: Wine\n\n書き直し。")); !reflect.DeepEqual(got, []string{"p1", "w1"}) {
		t.Fatalf("a rewrite cites %v, want its own posts too", got)
	}
	if got := ids(prov.forWrite("tastes/whites.md", "白の傾向。根拠: [Altura](/wines/altura)")); !reflect.DeepEqual(got, []string{"p1", "w1"}) {
		t.Fatalf("a note linking to another cites %v", got)
	}
	prov.noteForgotten("tastes/larrivet.md")
	if got := ids(prov.forWrite("tastes/merged.md", "統合。")); !reflect.DeepEqual(got, []string{"old1", "p1"}) {
		t.Fatalf("a note replacing a forgotten one cites %v", got)
	}
	if got := prov.forWrite("index.md", "# 目次"); got != nil {
		t.Fatalf("the index cites %v, want nothing", got)
	}
	// A note thrown away after the write still passes its posts to what was
	// written this turn.
	prov.noteWritten("tastes/merged.md")
	prov.noteWritten("index.md")
	sources, to := prov.passOn("wines/altura.md")
	if ids(sources)[0] != "w1" || !reflect.DeepEqual(to, []string{"tastes/merged.md"}) {
		t.Fatalf("passed %v to %v", ids(sources), to)
	}
	var none *provenance
	if none.forWrite("a.md", "") != nil {
		t.Fatal("a nil provenance is not harmless")
	}
}
