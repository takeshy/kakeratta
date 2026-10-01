package tools

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/docs/system"
)

func TestKakerattaHelpReadsTheShippedOKFBundle(t *testing.T) {
	ctx := context.Background()
	set := New(Config{}).For([]string{"kakeratta_help"})
	if !set.IsReadOnly("kakeratta_help") {
		t.Fatal("specification reads must be available during research")
	}
	index, err := set.Run(ctx, "kakeratta_help", Args{}, nil)
	if err != nil || !strings.Contains(index.Text, `okf_version: "0.2"`) {
		t.Fatalf("index = %+v, err = %v", index, err)
	}
	// The shipped index must lead to readable chapters, with OKF metadata.
	links := regexp.MustCompile(`\]\(\./([a-z]+\.md)\)`).FindAllStringSubmatch(index.Text, -1)
	if len(links) != 6 {
		t.Fatalf("index chapters = %v", links)
	}
	for _, link := range links {
		name := link[1]
		result, err := set.Run(ctx, "kakeratta_help", Args{"path": name}, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, err := system.Bundle.ReadFile(name)
		if err != nil || !strings.HasSuffix(result.Text, string(raw)) {
			t.Fatalf("%s did not return the complete shipped chapter", name)
		}
		if !strings.Contains(result.Text, "\ntype:") || !strings.Contains(result.Text, "\nupdated:") {
			t.Errorf("%s lacks chapter metadata", name)
		}
	}
}

func TestKakerattaHelpRefusesOtherFilesAndUndeclaredAccess(t *testing.T) {
	ctx := context.Background()
	r := New(Config{})
	set := r.For([]string{"kakeratta_help"})
	for _, path := range []any{"", "../system/index.md", "/etc/passwd", "./index.md", "bundle.go", "missing.md", nil, 1} {
		if result, err := set.Run(ctx, "kakeratta_help", Args{"path": path}, nil); err == nil || result.Text != "" {
			t.Errorf("path %v returned %+v, err = %v", path, result, err)
		}
	}
	if _, err := r.For(nil).Run(ctx, "kakeratta_help", Args{}, nil); err == nil {
		t.Fatal("undeclared specification access was allowed")
	}
}
