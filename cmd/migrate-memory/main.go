// Splits the personas' prose field summaries into facts, once, after the
// deploy that brought the facts. It runs in two steps so that what is written
// is what was read:
//
//  1. Without -apply, each persona's model splits its summaries into facts;
//     they are printed and saved to -plan (JSON), and nothing is changed. The
//     plan can be edited by hand.
//  2. With -apply, the facts in -plan are written as they are, without calling
//     the model, and the prose kept as each field's summary. A persona whose
//     prose changed since step 1 is refused.
//
// Local workspace (KAKERATTA_HOME or the default):
//
//	go run ./cmd/migrate-memory -plan plan.json [-apply]
//
// Production Firestore (application default credentials):
//
//	go run ./cmd/migrate-memory -firestore kakeratta -plan plan.json [-user KEY] [-apply]
package main

import (
	"context"
	"encoding/json"
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
	apply := flag.Bool("apply", false, "write the facts saved in -plan in place of the prose (default: make the plan)")
	planFile := flag.String("plan", "migration-plan.json", "where the dry run saves the facts, and -apply reads them")
	project := flag.String("firestore", "", "Firestore project ID; empty uses the local workspace")
	user := flag.String("user", "", "workspace key in Firestore; empty runs every workspace")
	only := flag.String("persona", "", "one persona ID; empty runs every persona")
	flag.Parse()
	ctx := context.Background()
	plan := map[string][]persona.Migration{}
	if *apply {
		raw, err := os.ReadFile(*planFile)
		if err != nil {
			log.Fatalf("-apply には dry-run で作った -plan が要ります: %v", err)
		}
		if err := json.Unmarshal(raw, &plan); err != nil {
			log.Fatal(err)
		}
	}
	save := func() {
		if *apply {
			return
		}
		raw, _ := json.MarshalIndent(plan, "", "  ")
		if err := os.WriteFile(*planFile, raw, 0o600); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("# plan saved to %s\n", *planFile)
	}

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
		results, err := migrate(ctx, workspace, *only, *apply, plan["local"])
		plan["local"] = results
		save()
		if err != nil {
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
		results, err := migrate(ctx, workspace, *only, *apply, plan[key])
		plan[key] = results
		if err != nil {
			log.Printf("%s: %v", key, err)
			failed = true
		}
		_ = workspace.Close()
	}
	save()
	if failed {
		os.Exit(1)
	}
}

// migrate makes the plan for every persona of one workspace, on the
// workspace's own model settings as the server would, or applies the plan it
// is given.
func migrate(ctx context.Context, workspace store.Workspace, only string, apply bool, planned []persona.Migration) ([]persona.Migration, error) {
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
	var results []persona.Migration
	var failures []string
	if apply {
		for _, migration := range planned {
			if only != "" && migration.Persona != only {
				continue
			}
			if len(migration.Legacy) == 0 || migration.Applied {
				continue
			}
			done, err := runner.ApplyMigration(ctx, migration)
			if err != nil {
				fmt.Printf("## %s: 失敗 %v\n\n", migration.Persona, err)
				failures = append(failures, migration.Persona)
				continue
			}
			report(done)
			results = append(results, done)
		}
	} else {
		ids := runner.MigrationPersonas(ctx)
		if only != "" {
			ids = []string{only}
		}
		for _, id := range ids {
			result, err := runner.MigrateMemory(ctx, id)
			if err != nil {
				fmt.Printf("## %s: 失敗 %v\n\n", id, err)
				failures = append(failures, id)
				continue
			}
			report(result)
			if len(result.Legacy) > 0 {
				results = append(results, result)
			}
		}
	}
	if len(failures) > 0 {
		return results, fmt.Errorf("失敗した担当: %s", strings.Join(failures, ", "))
	}
	return results, nil
}

func report(result persona.Migration) {
	if len(result.Legacy) == 0 {
		fmt.Printf("## %s: 文章のまとめはありません\n\n", result.Persona)
		return
	}
	state := "dry-run"
	if result.Applied {
		state = "適用済み"
	}
	fmt.Printf("## %s（%s）: まとめ %s\n", result.Persona, state, strings.Join(result.Legacy, ", "))
	var fields []string
	for field := range result.Facts {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	if len(fields) == 0 {
		fmt.Println("\n（事実はありません。まとめは消えます）")
	}
	for _, field := range fields {
		fmt.Printf("\n### %s\n", field)
		if summary := result.Summaries[field]; summary != "" {
			fmt.Printf("まとめ文（そのまま残す）:\n%s\n\n事実:\n", summary)
		}
		for _, fact := range result.Facts[field] {
			fmt.Printf("- %s\n", fact)
		}
	}
	fmt.Println()
}
