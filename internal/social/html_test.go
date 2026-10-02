package social

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestHTMLRecognitionMatchesBrowser(t *testing.T) {
	html := "<!DOCTYPE html><html><body>図</body></html>"
	for _, text := range []string{html, "```html\n" + html + "\n```", "  " + html + "  "} {
		if HTMLDocument(text) != html {
			t.Fatalf("not recognized: %q", text)
		}
	}
	for _, text := range []string{"<div>図</div>", "example <html>code</html>", "<html>unfinished", "```html\n<div>code</div>\n```"} {
		if HTMLDocument(text) != "" {
			t.Fatalf("converted prose: %q", text)
		}
	}
}

func TestHTMLPlanUsesImagesKeepsParentsAndSealsSource(t *testing.T) {
	entries := conversation()
	entries[1].Text = "<html><body>図</body></html>"
	entries[1].Sources = nil
	label := func(string) string { return "🍚 tabe" }
	chosen := []string{"p", "a", "c"}
	if _, err := PlanSelection("bluesky", "p", entries, chosen, label); err == nil {
		t.Fatal("HTML source published without conversion")
	}
	rendered := map[string]HTMLImages{"a": {SourceHash: fmt.Sprintf("%x", sha256.Sum256([]byte(entries[1].Text))), Images: []string{"page1", "page2"}, Alt: "図"}}
	plan, err := PlanSelectionWithImages("bluesky", "p", entries, chosen, label, rendered)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Posts) != 4 || plan.Posts[1].Image != "page1" || plan.Posts[2].Image != "page2" || plan.Posts[2].Parent != "a" || plan.Posts[3].Parent != "a:image:2" {
		t.Fatalf("image chain: %+v", plan.Posts)
	}
	for _, post := range plan.Posts {
		if strings.Contains(post.Text, "<html>") {
			t.Fatal("HTML leaked into text")
		}
	}
	if plan.Posts[1].ImageAlt != "図" {
		t.Fatal("missing alt")
	}
	rendered["a"] = HTMLImages{SourceHash: rendered["a"].SourceHash, Images: []string{"changed", "page2"}}
	changed, err := PlanSelectionWithImages("bluesky", "p", entries, chosen, label, rendered)
	if err != nil || changed.Digest == plan.Digest {
		t.Fatal("changed image accepted with old digest")
	}
	entries[1].Text = "<html><body>updated</body></html>"
	if _, err := PlanSelectionWithImages("bluesky", "p", entries, chosen, label, rendered); err == nil {
		t.Fatal("stale source accepted")
	}
	// HTML in a user post stays literal text, matching timeline rendering.
	entries[0].Text = "<html><body>example</body></html>"
	user, err := PlanSelection("bluesky", "p", entries, []string{"p"}, label)
	if err != nil || user.Posts[0].Text != entries[0].Text {
		t.Fatal("user code was converted")
	}
}
