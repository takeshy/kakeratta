package persona

import (
	"encoding/json"
	"testing"

	"github.com/takeshy/dokking/internal/book"
)

func TestAPluginDeclaresNoTextAccess(t *testing.T) {
	// What a persona may read is settled by the book's category, so there is
	// nothing about it to declare. If a field for it ever appears here, the
	// boundary has become something a plugin can argue with.
	raw, err := json.Marshal(Builtins[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"access", "scope", "boundary", "readWholeBook", "spoilers"} {
		if _, found := fields[name]; found {
			t.Fatalf("a plugin must not declare %q", name)
		}
	}
	if _, found := fields["webSearch"]; !found {
		t.Fatal("web search is the one outward-facing thing and must be declared")
	}
}

func TestPersonasAreOfferedByCategory(t *testing.T) {
	catalogue := NewCatalogue(t.TempDir())
	novel := catalogue.For(book.CategoryNovel)
	if len(novel) == 0 {
		t.Fatal("no novel personas ship with the app")
	}
	for _, plugin := range novel {
		if plugin.Suits(book.CategoryTechnical) {
			t.Fatalf("%s is offered for both categories", plugin.ID)
		}
	}
	technical := catalogue.For(book.CategoryTechnical)
	if len(technical) == 0 {
		t.Fatal("no technical personas ship with the app")
	}
}

func TestInstallRejectsWhatItCannotTrust(t *testing.T) {
	catalogue := NewCatalogue(t.TempDir())
	cases := map[string]string{
		"パス区切りを含むID": `{"id":"../evil","name":"x","tone":"y","categories":["novel"]}`,
		"カテゴリ無し":     `{"id":"ok","name":"x","tone":"y","categories":[]}`,
		"知らないカテゴリ":   `{"id":"ok","name":"x","tone":"y","categories":["everything"]}`,
		"内蔵と同じID":    `{"id":"mikage","name":"x","tone":"y","categories":["novel"]}`,
		"名前が無い":      `{"id":"ok","name":"","tone":"y","categories":["novel"]}`,
	}
	for name, definition := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := catalogue.Install([]byte(definition)); err == nil {
				t.Fatal("installed something it should have refused")
			}
		})
	}
}

func TestInstalledPersonasJoinTheCatalogue(t *testing.T) {
	catalogue := NewCatalogue(t.TempDir())
	plugin, err := catalogue.Install([]byte(
		`{"id":"tsumugi","name":"つむぎ","icon":"📚","tone":"静かに読む","categories":["novel"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if plugin.Builtin {
		t.Fatal("an installed persona is not a built-in one")
	}
	found, ok := catalogue.Find("tsumugi")
	if !ok || found.Name != "つむぎ" {
		t.Fatalf("installed persona not in the catalogue: %+v", found)
	}
	if err := catalogue.Uninstall("tsumugi"); err != nil {
		t.Fatal(err)
	}
	if _, ok := catalogue.Find("tsumugi"); ok {
		t.Fatal("uninstalled persona is still offered")
	}
	// The ones that ship with the app are not the reader's to delete.
	if err := catalogue.Uninstall("mikage"); err == nil {
		t.Fatal("a built-in persona was removed")
	}
}

// "This may be out of date" is worse than silence when it is wrong, so the page
// event only fires on the kinds of statement that actually go stale.
func TestStaleOnlyFiresOnWhatGoesOutOfDate(t *testing.T) {
	if terms := stale("Rails 7.0 では config.load_defaults を使います"); len(terms) == 0 {
		t.Fatal("a version number should be worth checking")
	}
	if terms := stale("このメソッドは非推奨です"); len(terms) == 0 {
		t.Fatal("a deprecation notice should be worth checking")
	}
	if terms := stale("オブジェクト指向とは、データと手続きをひとまとまりに扱う考え方です。"); len(terms) != 0 {
		t.Fatalf("nothing here goes stale, but got %v", terms)
	}
}
