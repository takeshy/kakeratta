// Folds the personas' old one-note-per-thing memory into field summaries, once,
// after the deploy that brought the fields. Without -apply it only prints what
// each persona's model would write; with -apply it writes the summaries and
// deletes the old notes.
//
// Local workspace (KAKERATTA_HOME or the default):
//
//	go run ./cmd/migrate-memory [-apply]
//
// Production Firestore (application default credentials):
//
//	go run ./cmd/migrate-memory -firestore kakeratta [-user KEY] [-apply]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

func main() {
	apply := flag.Bool("apply", false, "write the summaries and delete the old notes (default: only show them)")
	project := flag.String("firestore", "", "Firestore project ID; empty uses the local workspace")
	user := flag.String("user", "", "workspace key in Firestore; empty runs every workspace")
	only := flag.String("persona", "", "one persona ID; empty runs every persona")
	flag.Parse()
	ctx := context.Background()

	if *project == "" {
		root := os.Getenv("KAKERATTA_HOME")
		if root == "" {
			root = store.DefaultRoot()
		}
		workspace, err := store.Open(root)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("# workspace %s\n", root)
		if err := migrate(ctx, workspace, *only, *apply); err != nil {
			log.Fatal(err)
		}
		return
	}

	client, err := firestore.NewClient(ctx, *project)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	keys := []string{*user}
	if *user == "" {
		keys = nil
		docs := client.Collection("users").DocumentRefs(ctx)
		for {
			ref, err := docs.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				log.Fatal(err)
			}
			keys = append(keys, ref.ID)
		}
		sort.Strings(keys)
	}
	failed := false
	for _, key := range keys {
		workspace, err := store.NewFirestoreWorkspace(ctx, client, key, nil)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("# workspace %s\n", key)
		if err := migrate(ctx, workspace, *only, *apply); err != nil {
			log.Printf("%s: %v", key, err)
			failed = true
		}
		_ = workspace.Close()
	}
	if failed {
		os.Exit(1)
	}
}

// migrate runs every persona of one workspace, on the workspace's own model
// settings, as the server would.
func migrate(ctx context.Context, workspace store.Workspace, only string, apply bool) error {
	clientOf := func() (*llm.Client, store.Settings) {
		settings := workspace.Settings()
		if settings.OpenRouterMode && strings.TrimSpace(settings.OpenRouterAPIKey) != "" {
			settings.MiddleModel = settings.OpenRouterMiddleModel
			settings.LowModel = settings.OpenRouterLowModel
			return llm.NewOpenRouter(store.OpenRouterBaseURL, settings.OpenRouterAPIKey), settings
		}
		return llm.New(settings.BaseURL, settings.APIKey), settings
	}
	runner := persona.New(workspace, persona.NewCatalogue(workspace), clientOf)
	ids := runner.MigrationPersonas(ctx)
	if only != "" {
		ids = []string{only}
	}
	var failures []string
	for _, id := range ids {
		result, err := runner.MigrateMemory(ctx, id, apply)
		if err != nil {
			fmt.Printf("## %s: 失敗 %v\n\n", id, err)
			failures = append(failures, id)
			continue
		}
		if len(result.Legacy) == 0 {
			fmt.Printf("## %s: 古いメモはありません\n\n", id)
			continue
		}
		state := "dry-run"
		if result.Applied {
			state = "適用済み"
		}
		fmt.Printf("## %s（%s）: 古いメモ %d件", id, state, len(result.Legacy))
		if len(result.Omitted) > 0 {
			fmt.Printf("、うち読み切れず省いたもの %d件: %s", len(result.Omitted), strings.Join(result.Omitted, ", "))
		}
		fmt.Println()
		var fields []string
		for field := range result.Fields {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			fmt.Printf("\n### %s.md（%d字）\n%s\n", field, len([]rune(result.Fields[field])), result.Fields[field])
		}
		fmt.Println()
	}
	if len(failures) > 0 {
		return fmt.Errorf("失敗した担当: %s", strings.Join(failures, ", "))
	}
	return nil
}
