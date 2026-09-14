package social

import (
	"github.com/takeshy/kakeratta/internal/timeline"
	"strings"
	"testing"
)

func conversation() []timeline.Entry {
	return []timeline.Entry{
		{ID: "p", Kind: timeline.KindPost, Speaker: "user", Text: "麻婆豆腐", Place: &timeline.Place{Label: "自宅"}, Media: &timeline.Media{BlobID: "photo", Text: "private OCR", Describe: "private description"}},
		{ID: "a", Root: "p", ReplyTo: "p", Speaker: "tabe", Text: "辛そう"},
		{ID: "b", Root: "p", ReplyTo: "p", Speaker: "gout", Text: "private health"},
		{ID: "c", Root: "p", ReplyTo: "a", Speaker: "user", Text: "また食べたい"},
		{ID: "d", Root: "p", ReplyTo: "c", Speaker: "tabe", Text: "いいですね"},
	}
}
func TestSelectionKeepsBranchesAndOnlyExplicitContent(t *testing.T) {
	p, err := PlanSelection("bluesky", "p", conversation(), []string{"d", "c", "a", "p"}, func(string) string { return "🍚 tabe" })
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Posts) != 4 || p.Posts[1].Text != "🍚 tabeからの返信:\n辛そう" || p.Posts[3].Parent != "c" {
		t.Fatalf("%+v", p)
	}
	if p.Posts[0].Text != "麻婆豆腐" || p.Posts[0].BlobID != "photo" {
		t.Fatal(p.Posts[0])
	}
	p, err = PlanSelection("bluesky", "p", conversation(), []string{"p", "a", "b"}, func(id string) string { return id })
	if err != nil || p.Posts[1].Parent != "p" || p.Posts[2].Parent != "p" {
		t.Fatalf("branches: %+v %v", p, err)
	}
}
func TestRejectsOmittedAncestorsDeletedAndForeignEntries(t *testing.T) {
	for _, ids := range [][]string{{"p", "c"}, {"a"}, {"p", "missing"}, {}} {
		if _, err := PlanSelection("bluesky", "p", conversation(), ids, func(s string) string { return s }); err == nil {
			t.Fatalf("accepted %v", ids)
		}
	}
	entries := conversation()
	entries[0].Deleted = true
	if _, err := PlanSelection("bluesky", "p", entries, []string{"p"}, nil); err == nil {
		t.Fatal("deleted root")
	}
	entries = conversation()
	entries[1].ReplyTo = "c"
	if _, err := PlanSelection("bluesky", "p", entries, []string{"p", "a", "c"}, func(s string) string { return s }); err == nil {
		t.Fatal("cycle")
	}
}
func TestLimitsIncludePersonaPrefixAndDigestChanges(t *testing.T) {
	entries := conversation()
	entries[1].Text = strings.Repeat("あ", 300)
	if plan, err := PlanSelection("bluesky", "p", entries, []string{"p", "a"}, func(s string) string { return s }); err != nil || len(plan.Posts) != 3 {
		t.Fatalf("prefix must count towards splitting: %+v %v", plan, err)
	}
	a, _ := PlanSelection("bluesky", "p", entries, []string{"p"}, nil)
	entries[0].Text = "changed"
	b, _ := PlanSelection("bluesky", "p", entries, []string{"p"}, nil)
	if a.Digest == b.Digest {
		t.Fatal("stale preview accepted")
	}
}
