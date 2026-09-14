package social

import (
	"github.com/rivo/uniseg"
	"strings"
	"testing"
)

func TestSplitPreservesTextAndPrefersCompleteSentences(t *testing.T) {
	sentence := strings.Repeat("あ", 179) + "。"
	text := sentence + sentence + "\n" + sentence
	parts, err := SplitText(text, "🍚 tabeからの返信:\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("%q", parts)
	}
	var restored string
	for i, part := range parts {
		prefix := "🍚 tabeからの返信:\n"
		if i > 0 {
			prefix = continuation + prefix
		}
		if !strings.HasPrefix(part, prefix) || uniseg.GraphemeClusterCount(part) > 300 || len(part) > 3000 {
			t.Fatal(part)
		}
		restored += strings.TrimPrefix(part, prefix)
	}
	if restored != text {
		t.Fatal("content lost or duplicated")
	}
	if parts[0] != "🍚 tabeからの返信:\n"+sentence {
		t.Fatal("split inside sentence")
	}
}
func TestSplitBoundariesAndUnbreakableUnits(t *testing.T) {
	for _, text := range []string{strings.Repeat("あ", 300), strings.Repeat("👨‍👩‍👧‍👦", 299), strings.Repeat("a", 299) + "e\u0301", strings.Repeat("あ", 650), strings.Repeat("word ", 130)} {
		parts, err := SplitText(text, "")
		if err != nil {
			t.Fatal(err)
		}
		var restored string
		for i, part := range parts {
			if uniseg.GraphemeClusterCount(part) > 300 || len(part) > 3000 {
				t.Fatal("exceeded limit")
			}
			if i > 0 {
				part = strings.TrimPrefix(part, continuation)
			}
			restored += part
		}
		if restored != text {
			t.Fatal("broken grapheme or lost content")
		}
	}
	text := strings.Repeat("あ", 270) + "。\nhttps://example.com/" + strings.Repeat("x", 60)
	parts, err := SplitText(text, "")
	if err != nil || len(parts) != 2 || !strings.Contains(parts[1], "https://example.com/"+strings.Repeat("x", 60)) {
		t.Fatalf("URL split: %q %v", parts, err)
	}
	if _, err = SplitText("https://example.com/"+strings.Repeat("x", 400), ""); err == nil {
		t.Fatal("overlong URL silently broken")
	}
}
func TestSplitPlanChainsPartsAndBranchesFromParentTail(t *testing.T) {
	entries := conversation()
	entries[0].Text = strings.Repeat("文。", 200)
	entries[1].Text = strings.Repeat("返事。", 150)
	plan, err := PlanSelection("bluesky", "p", entries, []string{"p", "a", "b", "c"}, func(string) string { return "🍚 tabe" })
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Posts) != 6 {
		t.Fatalf("%+v", plan.Posts)
	}
	want := []string{"", "p", "p:part:2", "a", "p:part:2", "a:part:2"}
	for i, post := range plan.Posts {
		if post.Parent != want[i] {
			t.Fatalf("%d parent=%s want=%s", i, post.Parent, want[i])
		}
	}
	if plan.Posts[0].Key() != "p" || plan.Posts[0].BlobID != "photo" || plan.Posts[1].BlobID != "" {
		t.Fatal("legacy key or first-only attachment changed")
	}
}
