package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Every facet must cover exactly its label: byte ranges are what Bluesky reads.
func covered(t *testing.T, text string, links []Link) []string {
	t.Helper()
	var labels []string
	for _, link := range links {
		if link.Start < 0 || link.End > len(text) || link.Start >= link.End {
			t.Fatalf("facet %+v outside %q", link, text)
		}
		labels = append(labels, text[link.Start:link.End]+"="+link.URI)
	}
	return labels
}

func TestMarkdownReadsAsPlainTextWithLinkFacets(t *testing.T) {
	text, links := FromMarkdown(strings.Join([]string{
		"  # 📊 **売上**の比較",
		"詳しくは[公式発表](https://example.com/news)と`資料`を参照。",
		"",
		"## 📍 店舗",
		"- 🚶 A店",
		"  - 駅前",
		"1. 確認する",
		"- [x] 予約済み",
		"| 店 | 予算 |",
		"| --- | --- |",
		"| A店 | 1000円 |",
		"> [!tip] 💡 早めに行く",
		"> 引用です",
		"---",
		"```mermaid",
		"flowchart TD",
		"A[⏱️ 急ぐ?] --> B[A店]",
		"A -->|余裕| C[B店]",
		"B -- 近い --> C",
		"```",
		"```mermaid",
		"sequenceDiagram",
		"participant U as ユーザー",
		"U->>S: 予約",
		"```",
		"```mermaid",
		"pie title 割合",
		"```",
		"[危険](javascript:alert)",
	}, "\n"))
	want := strings.Join([]string{
		"📊 売上の比較",
		"詳しくは公式発表と資料を参照。",
		"",
		"📍 店舗",
		"・🚶 A店",
		"　・駅前",
		"1. 確認する",
		"☑ 予約済み",
		"店 ｜ 予算",
		"・A店 ｜ 1000円",
		"💡 早めに行く",
		"＞ 引用です",
		"",
		"・⏱️ 急ぐ? → A店",
		"・⏱️ 急ぐ? → B店（余裕）",
		"・A店 → B店（近い）",
		"・ユーザー → S: 予約",
		"危険",
	}, "\n")
	if text != want {
		t.Fatalf("text =\n%s\nwant\n%s", text, want)
	}
	if labels := covered(t, text, links); len(labels) != 1 || labels[0] != "公式発表=https://example.com/news" {
		t.Fatalf("links = %v", labels)
	}
}

func TestSplitKeepsLinksWholeAndShiftsFacetsIntoEachPart(t *testing.T) {
	text := strings.Repeat("あ", 280) + "。[[リンク]] と https://example.com/x の後。"
	label := strings.Index(text, "[[リンク]]")
	parts, err := SplitRich(text, "📊 担当からの返信:\n", []Link{{Start: label, End: label + len("[[リンク]]"), URI: "https://example.com/label"}})
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts = %+v, %v", parts, err)
	}
	if len(parts[0].Links) != 0 {
		t.Fatalf("first part links = %+v", parts[0].Links)
	}
	if labels := covered(t, parts[1].Text, parts[1].Links); len(labels) != 2 ||
		labels[0] != "[[リンク]]=https://example.com/label" || labels[1] != "https://example.com/x=https://example.com/x" {
		t.Fatalf("second part %q links = %v", parts[1].Text, labels)
	}
}

func TestPlanPostsMarkdownAnswersAsRichTextAndHTMLStaysAnImage(t *testing.T) {
	entries := []timeline.Entry{
		{ID: "p", Kind: timeline.KindPost, Speaker: "user", Text: "**そのまま** https://example.com/u"},
		{ID: "a", Root: "p", ReplyTo: "p", Speaker: "infographic", Text: "# 📊 図解\n- [出典](https://example.com/s)"},
	}
	p, err := PlanSelection("bluesky", "p", entries, []string{"p", "a"}, func(string) string { return "📊 図" })
	if err != nil {
		t.Fatal(err)
	}
	if p.Posts[0].Text != "**そのまま** https://example.com/u" {
		t.Fatalf("user text was rewritten: %q", p.Posts[0].Text)
	}
	if labels := covered(t, p.Posts[0].Text, p.Posts[0].Links); len(labels) != 1 || labels[0] != "https://example.com/u=https://example.com/u" {
		t.Fatalf("user links = %v", labels)
	}
	if p.Posts[1].Text != "📊 図からの返信:\n📊 図解\n・出典" {
		t.Fatalf("answer = %q", p.Posts[1].Text)
	}
	if labels := covered(t, p.Posts[1].Text, p.Posts[1].Links); len(labels) != 1 || labels[0] != "出典=https://example.com/s" {
		t.Fatalf("answer links = %v", labels)
	}
	// The facets are sealed with the plan.
	before := p.Digest
	p.Posts[1].Links[0].URI = "https://example.com/other"
	p.Seal()
	if p.Digest == before {
		t.Fatal("digest ignored the links")
	}
	html := "<!DOCTYPE html><html><body><h1>図解</h1></body></html>"
	entries[1].Text = html
	if _, err := PlanSelection("bluesky", "p", entries, []string{"a"}, func(string) string { return "図" }); err == nil {
		t.Fatal("an HTML answer was planned as text without its image")
	}
}

func TestPublishSendsLinkFacets(t *testing.T) {
	var record map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Record map[string]any `json:"record"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		record = body.Record
		fmt.Fprint(w, `{"uri":"at://did:plc:test/app.bsky.feed.post/abc","cid":"cid"}`)
	}))
	defer srv.Close()
	post := Post{Text: "見る: 出典", Links: []Link{{Start: len("見る: "), End: len("見る: 出典"), URI: "https://example.com/s"}, {Start: 0, End: 99, URI: "https://example.com/bad"}}}
	if _, err := testClient(srv.URL).Publish(context.Background(), post, store.Publication{}, store.Publication{}, nil, ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(record["facets"])
	if string(raw) != `[{"features":[{"$type":"app.bsky.richtext.facet#link","uri":"https://example.com/s"}],"index":{"byteEnd":14,"byteStart":8}}]` {
		t.Fatalf("facets = %s", raw)
	}
}

func TestMermaidArrowsReadTheSyntaxModelsWrite(t *testing.T) {
	flow := mermaidArrows(strings.Split(strings.Join([]string{
		"flowchart TD",
		"  subgraph Morning[朝]",
		"    A[🔬 Research",
		"    Cursor+Hex] -->|旧| B{判断}",
		"  end",
		"  B -- 新 --> C[完了<br>送信] --> D(終わり)",
		"  C & D -.->|連携| E",
		"  E --- F",
	}, "\n"), "\n"))
	want := []string{
		"・🔬 Research Cursor+Hex → 判断（旧）",
		"・判断 → 完了 送信（新）",
		"・完了 送信 → 終わり",
		"・完了 送信 → E（連携）",
		"・終わり → E（連携）",
		"・E ― F",
	}
	if strings.Join(flow, "\n") != strings.Join(want, "\n") {
		t.Fatalf("flowchart =\n%s", strings.Join(flow, "\n"))
	}
	sequence := mermaidArrows([]string{
		"sequenceDiagram", "participant H as 👤 Human", "Note over H: 夜間に準備", "loop 毎朝",
		"H->>B: 全件を", "3システム照合", "B-->>H: 結果", "H->>H: 判断", "end", "B->>🔗 John Ford: 送信",
	})
	want = []string{"・📝 夜間に準備", "・👤 Human → B: 全件を 3システム照合", "・B → 👤 Human: 結果", "・👤 Human → 👤 Human: 判断", "・B → 🔗 John Ford: 送信"}
	if strings.Join(sequence, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sequence =\n%s", strings.Join(sequence, "\n"))
	}
	if mermaidArrows([]string{"pie title 割合", `"A" : 1`}) != nil {
		t.Fatal("an unsupported diagram was read")
	}
}
