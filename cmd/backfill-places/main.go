// Backfill location labels in Firestore without changing post bodies or routing.
// A dry run saves the exact changes; -apply uses document update-time preconditions.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/takeshy/kakeratta/internal/geocode"
	"github.com/takeshy/kakeratta/internal/mcp"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type change struct {
	Path    string         `json:"path"`
	Updated time.Time      `json:"updated"`
	Before  timeline.Place `json:"before"`
	After   timeline.Place `json:"after"`
}

type plan struct {
	Project      string   `json:"project"`
	Refresh      bool     `json:"refresh,omitempty"`
	MetadataOnly bool     `json:"metadataOnly,omitempty"`
	Changes      []change `json:"changes"`
}

func eligible(e timeline.Entry, refresh bool) bool {
	p := e.Place
	return !e.Deleted && e.Kind == timeline.KindPost && e.Speaker == timeline.User && p != nil && (p.Label == "" || refresh && p.Approximate) &&
		!math.IsNaN(p.Lat) && !math.IsNaN(p.Lon) && math.Abs(p.Lat) <= 90 && math.Abs(p.Lon) <= 180
}

func resolved(raw json.RawMessage, before timeline.Place, language string) (timeline.Place, bool, error) {
	return geocode.Resolve(raw, before, language)
}

func validChange(c change) bool {
	parts := strings.Split(c.Path, "/")
	return len(parts) == 4 && parts[0] == "users" && parts[1] != "" && parts[2] == "timeline" && parts[3] != "" &&
		!c.Updated.IsZero() && (c.Before.Label == "" || c.Before.Approximate) && c.After.Label != "" && c.After.Approximate &&
		c.Before.Lat == c.After.Lat && c.Before.Lon == c.After.Lon
}

func run() error {
	project := flag.String("firestore", "", "required Firestore project ID (default database)")
	user := flag.String("user", "", "workspace key; empty scans all workspaces")
	planFile := flag.String("plan", "place-plan.json", "dry-run output or apply input (contains coordinates; keep private)")
	config := flag.String("mcp-config", "", "file containing the operator MCP servers JSON array")
	refresh := flag.Bool("refresh", false, "also refresh existing approximate geocoded labels; custom labels remain unchanged")
	metadataOnly := flag.Bool("metadata-only", false, "refresh country and region while preserving existing display labels")
	scan := flag.Bool("scan", false, "count eligible posts only; no MCP calls or writes")
	apply := flag.Bool("apply", false, "apply an existing plan without MCP calls")
	verify := flag.Bool("verify", false, "read back locations and compare with an existing plan")
	flag.Parse()
	if *project == "" || (*scan && (*apply || *verify)) || (*apply && *verify) {
		return errors.New("specify -firestore; -scan, -apply and -verify are mutually exclusive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	client, err := firestore.NewClient(ctx, *project)
	if err != nil {
		return err
	}
	defer client.Close()
	if *apply || *verify {
		raw, err := os.ReadFile(*planFile)
		if err != nil {
			return err
		}
		var p plan
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.Project != *project {
			return errors.New("plan project does not match target")
		}
		for _, c := range p.Changes {
			if !validChange(c) || c.Before.Label != "" && !p.Refresh || p.MetadataOnly && c.Before.Label != "" && c.Before.Label != c.After.Label {
				return errors.New("invalid plan change")
			}
		}
		if *verify {
			matched, missing, different := 0, 0, 0
			for _, c := range p.Changes {
				if *user != "" && strings.Split(c.Path, "/")[1] != *user {
					continue
				}
				doc, err := client.Doc(c.Path).Get(ctx)
				if status.Code(err) == codes.NotFound {
					missing++
					continue
				}
				if err != nil {
					return err
				}
				var e timeline.Entry
				if err := doc.DataTo(&e); err != nil {
					return err
				}
				if e.Place == nil || *e.Place != c.After {
					different++
				} else {
					matched++
				}
			}
			fmt.Printf("verified=%d missing=%d different=%d\n", matched, missing, different)
			if missing != 0 || different != 0 {
				return errors.New("some planned locations did not match")
			}
			return nil
		}
		applied, skipped := 0, 0
		for _, c := range p.Changes {
			if *user != "" && strings.Split(c.Path, "/")[1] != *user {
				continue
			}
			_, err := client.Doc(c.Path).Update(ctx, []firestore.Update{
				{Path: "Place.Label", Value: c.After.Label},
				{Path: "Place.Approximate", Value: c.After.Approximate},
				{Path: "Place.GeonameID", Value: c.After.GeonameID},
				{Path: "Place.Country", Value: c.After.Country},
				{Path: "Place.CountryCode", Value: c.After.CountryCode},
				{Path: "Place.Region", Value: c.After.Region},
			}, firestore.LastUpdateTime(c.Updated))
			if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.NotFound {
				skipped++
				continue
			}
			if err != nil {
				return fmt.Errorf("after applied=%d skipped=%d: %w", applied, skipped, err)
			}
			applied++
		}
		fmt.Printf("applied=%d skipped_changed_or_deleted=%d\n", applied, skipped)
		return nil
	}
	var catalogue *mcp.Catalogue
	tool := ""
	if !*scan {
		raw, err := os.ReadFile(*config)
		if err != nil {
			return err
		}
		servers, err := mcp.ParseServers(string(raw))
		if err != nil {
			return err
		}
		catalogue = mcp.NewCatalogue(servers, "backfill-places")
		for _, listed := range catalogue.ToolsContext(ctx) {
			if listed.Name == "reverse_geocode" {
				tool = listed.QualifiedName()
				break
			}
		}
		if tool == "" {
			return errors.New("reverse_geocode is unavailable")
		}
	}
	var docs *firestore.DocumentIterator
	if *user == "" {
		docs = client.CollectionGroup("timeline").Documents(ctx)
	} else {
		docs = client.Collection("users").Doc(*user).Collection("timeline").Documents(ctx)
	}
	defer docs.Stop()
	p := plan{Project: *project, Refresh: *refresh || *metadataOnly, MetadataOnly: *metadataOnly}
	total, candidates, noMatch := 0, 0, 0
	for {
		doc, err := docs.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return err
		}
		path := strings.TrimPrefix(doc.Ref.Path, strings.TrimSuffix(client.Collection("users").Path, "users"))
		parts := strings.Split(path, "/")
		if len(parts) != 4 || parts[0] != "users" || parts[2] != "timeline" {
			continue
		}
		var e timeline.Entry
		if err := doc.DataTo(&e); err != nil {
			return err
		}
		total++
		if !eligible(e, *refresh || *metadataOnly) {
			continue
		}
		candidates++
		if *scan {
			continue
		}
		callCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		result, err := catalogue.Call(callCtx, tool, map[string]any{"lat": e.Place.Lat, "lng": e.Place.Lon, "language": geocode.Language(e.Language)})
		stop()
		if err != nil {
			return fmt.Errorf("lookup %d failed: %w", candidates, err)
		}
		if result.IsError {
			return fmt.Errorf("lookup %d returned an MCP error", candidates)
		}
		after, found, err := resolved(result.StructuredContent, *e.Place, e.Language)
		if err != nil {
			return err
		}
		if !found {
			noMatch++
			continue
		}
		if *metadataOnly && e.Place.Label != "" {
			after.Label, after.Approximate, after.GeonameID = e.Place.Label, e.Place.Approximate, e.Place.GeonameID
		}
		if after == *e.Place {
			continue
		}
		p.Changes = append(p.Changes, change{Path: path, Updated: doc.UpdateTime, Before: *e.Place, After: after})
		if candidates%25 == 0 {
			fmt.Printf("resolved=%d\n", len(p.Changes))
		}
	}
	fmt.Printf("entries=%d eligible=%d resolved=%d no_match=%d\n", total, candidates, len(p.Changes), noMatch)
	if *scan {
		return nil
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	// Refuse to replace a previous plan or its backup of the original places.
	f, err := os.OpenFile(*planFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	closeErr := f.Close()
	return errors.Join(err, closeErr)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
