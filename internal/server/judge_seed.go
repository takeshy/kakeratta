package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// English examples adapted from the owner's October 2026 production timeline.
// Runtime credentials, delivery jobs and device registrations are never copied.
//
//go:embed judge_seed.json
var judgeSeedJSON []byte

//go:embed judge_seed_assets/*.jpg
var judgeSeedAssets embed.FS

const judgeSeedMarker = "judge-english-seed-v1"

type judgeSeed struct {
	Personas         []persona.Skill `json:"personas"`
	Active           []string        `json:"active"`
	ExternalPersonas []string        `json:"externalPersonas"`
	Entries          []struct {
		ID       string          `json:"id"`
		Speaker  string          `json:"speaker"`
		Text     string          `json:"text"`
		Root     string          `json:"root"`
		ReplyTo  string          `json:"replyTo"`
		HoursAgo int             `json:"hoursAgo"`
		Image    string          `json:"image"`
		Media    *timeline.Media `json:"media"`
	} `json:"entries"`
	Memories []struct {
		Persona string `json:"persona"`
		Facts   []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
			From string `json:"from"`
		} `json:"facts"`
		Records []struct {
			From string `json:"from"`
			Text string `json:"text"`
			Tag  string `json:"tag"`
		} `json:"records"`
	} `json:"memories"`
}

type judgeSeedState struct {
	At    time.Time         `json:"at"`
	Done  bool              `json:"done"`
	Blobs map[string]string `json:"blobs,omitempty"`
}

// Seed only untouched invitation workspaces. A durable start time and stable IDs
// let a failed registration resume without duplicating content or moving dates.
// Completed or pre-existing workspaces are never reseeded, even after deletions.
func (s *Server) seedJudgeWorkspace(ctx context.Context) error {
	ctx, unlock, err := s.store.Lock(ctx, judgeSeedMarker)
	if err != nil {
		return err
	}
	defer unlock()
	marker, err := s.store.SocialValue(ctx, judgeSeedMarker)
	if err != nil {
		return err
	}
	var state judgeSeedState
	if len(marker.Data) != 0 {
		if err := json.Unmarshal(marker.Data, &state); err != nil {
			return err
		}
		if state.Done {
			return nil
		}
	} else {
		entries, err := s.store.Timeline()
		if err != nil {
			return err
		}
		skills, err := s.store.Skills(ctx)
		if err != nil {
			return err
		}
		state = judgeSeedState{At: time.Now().UTC().Truncate(time.Minute), Done: len(entries) > 0 || len(skills) > 0}
		data, _ := json.Marshal(state)
		ok, err := s.store.SwapSocialValue(ctx, judgeSeedMarker, marker.Version, store.SocialValue{Data: data})
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("judge seed marker changed")
		}
		if state.Done {
			return nil
		}
	}
	var seed judgeSeed
	if err := json.Unmarshal(judgeSeedJSON, &seed); err != nil {
		return err
	}
	installed, err := s.store.Skills(ctx)
	if err != nil {
		return err
	}
	for _, skill := range seed.Personas {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, exists := installed[skill.ID]; exists {
			continue
		}
		if err := persona.Validate(skill, nil); err != nil {
			return err
		}
		raw, err := json.Marshal(skill)
		if err != nil {
			return err
		}
		if err := s.store.PersistSkill(skill.ID, raw); err != nil {
			return err
		}
	}
	entries := make(map[string]timeline.Entry)
	for _, row := range seed.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := timeline.Entry{ID: row.ID, Speaker: row.Speaker, Text: row.Text, Language: "en", At: state.At.Add(-time.Duration(row.HoursAgo) * time.Hour), Root: row.Root, ReplyTo: row.ReplyTo, Kind: timeline.KindPost}
		if entry.Root == "" {
			entry.Root = entry.ID
		}
		if entry.ReplyTo != "" {
			entry.Kind = timeline.KindRemark
			if entry.Speaker == timeline.User {
				entry.Kind = timeline.KindReply
			}
		}
		if row.Image != "" {
			blobID := state.Blobs[row.Image]
			if blobID == "" {
				data, err := judgeSeedAssets.ReadFile("judge_seed_assets/" + row.Image)
				if err != nil {
					return err
				}
				blobID, err = s.store.PutBlob(data, "image/jpeg")
				if err != nil {
					return err
				}
				if state.Blobs == nil {
					state.Blobs = make(map[string]string)
				}
				state.Blobs[row.Image] = blobID
				marker, err := s.store.SocialValue(ctx, judgeSeedMarker)
				if err != nil {
					return err
				}
				raw, _ := json.Marshal(state)
				ok, err := s.store.SwapSocialValue(ctx, judgeSeedMarker, marker.Version, store.SocialValue{Data: raw})
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("judge seed media marker changed")
				}
			}
			if row.Media == nil {
				return fmt.Errorf("judge seed image has no metadata")
			}
			media := *row.Media
			media.BlobID, media.MIME = blobID, "image/jpeg"
			media.ReadAt = &entry.At
			entry.Media = &media
		}
		// No routing reaction: these are saved examples, not new AI requests.
		kept, _, err := s.store.CreateEntry(entry)
		if err != nil {
			return err
		}
		entries[entry.ID] = kept
	}
	for _, memory := range seed.Memories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, exists := s.store.Note(memory.Persona, persona.MemoPath); exists {
			continue
		}
		// The storage parser requires these headings; the English UI localizes them.
		text := "# Memo\n\n## いまの事実\n\n"
		var sources []store.NoteSource
		for _, fact := range memory.Facts {
			text += fmt.Sprintf("- [%s] %s from:%s since:%s\n", fact.ID, fact.Text, fact.From, entries[fact.From].At.Format("2006-01-02"))
			sources = append(sources, store.NoteSource{EntryID: fact.From, Label: entries[fact.From].Text})
		}
		text += "\n## かけらの記録\n\n"
		for _, record := range memory.Records {
			text += fmt.Sprintf("- %s [%s](/?entry=%s) %s #%s\n", entries[record.From].At.Format("2006-01-02 15:04"), record.From, record.From, record.Text, record.Tag)
			sources = append(sources, store.NoteSource{EntryID: record.From, Label: entries[record.From].Text})
		}
		if err := s.store.WriteNote(memory.Persona, persona.MemoPath, text, sources...); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	settings := s.store.ConfiguredSettings()
	settings.Active = append([]string(nil), seed.Active...)
	if len(s.judgeMCPKey) == 64 && s.judgeActive != nil && s.judgeActive() {
		if settings.Planners == nil {
			settings.Planners = make(map[string]string)
		}
		for _, id := range seed.ExternalPersonas {
			settings.Planners[id] = store.PlannerExternal
		}
	}
	if err := s.store.SaveSettings(settings); err != nil {
		return err
	}
	marker, err = s.store.SocialValue(ctx, judgeSeedMarker)
	if err != nil {
		return err
	}
	state.Done = true
	data, _ := json.Marshal(state)
	ok, err := s.store.SwapSocialValue(ctx, judgeSeedMarker, marker.Version, store.SocialValue{Data: data})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("judge seed completion changed")
	}
	return nil
}
