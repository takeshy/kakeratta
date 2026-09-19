package store

// Export copies one workspace out as the plain files a local kakeratta reads.
//
// The download a user takes in Settings is this: the same layout, the same file
// names, as KAKERATTA_HOME on a development machine, so extracting it and
// pointing a server at it reproduces the workspace. BlobStore travels through
// the store itself, so a workspace whose captures live in object storage still
// exports them as files.
//
// Nothing is assembled whole. The workspace is handed to an ExportSink one file
// at a time, because what writes the export is a small container with no disk
// of its own: a workspace larger than its memory has to pass through it, not
// sit in it.
//
// Search vectors are workspace files like any other and travel with the file
// store; a Firestore workspace leaves them behind and its exported timeline
// re-indexes on the first semantic search, the same recovery an index rebuild
// uses.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// ExportSink receives an export one file at a time and writes it wherever the
// export is going: straight down a response, or into an object in a bucket.
type ExportSink interface {
	// Add writes one file. name is slash-separated and relative to the
	// workspace root; modified is the file's own time, which is how a note
	// keeps the day it was written, and is zero for a file that has none.
	// write is called once, with the writer for this file's contents.
	Add(name string, modified time.Time, write func(io.Writer) error) error
}

// Export hands the whole file-backed workspace to sink. Every file under the
// workspace root travels as is; captures come through the blob store, which may
// be object storage rather than this directory.
func (s *Store) Export(ctx context.Context, sink ExportSink) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	blobs, err := s.exportFiles(ctx, sink)
	if err != nil {
		return err
	}
	return exportBlobs(ctx, blobs, sink)
}

// exportFiles lists the workspace once, then copies each file out. The list is
// taken under the lock; the copying is not, because an export can be slow and a
// post must not wait behind one.
func (s *Store) exportFiles(ctx context.Context, sink ExportSink) (BlobStore, error) {
	s.mu.RLock()
	blobs := s.blobs
	var names []string
	err := filepath.WalkDir(s.root, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(s.root, full)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			// Captures go through the blob store below, not this copy.
			if relative == "blobs" {
				return filepath.SkipDir
			}
			return nil
		}
		// A temporary file is a crash half way through a write, never state.
		if strings.HasSuffix(entry.Name(), ".tmp") {
			return nil
		}
		names = append(names, filepath.ToSlash(relative))
		return nil
	})
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := s.exportFile(name, sink); err != nil {
			return nil, err
		}
	}
	return blobs, nil
}

// exportFile hands one file to the sink. It is opened and measured under the
// lock and written out without it, and the copy stops at the length measured
// there: a line being appended at that moment is left whole for the next
// export rather than copied half written.
func (s *Store) exportFile(name string, sink ExportSink) error {
	s.mu.RLock()
	file, err := os.Open(s.path(filepath.FromSlash(name)))
	if err != nil {
		s.mu.RUnlock()
		// Deleted between the listing and now: it is not part of the workspace.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	info, err := file.Stat()
	s.mu.RUnlock()
	defer file.Close()
	if err != nil {
		return err
	}
	return sink.Add(name, info.ModTime(), func(w io.Writer) error {
		_, err := io.Copy(w, io.LimitReader(file, info.Size()))
		return err
	})
}

// Export hands the Firestore workspace to sink in the layout the file store
// uses, so the result is indistinguishable from a workspace that always lived
// on disk. Each collection becomes the file its file-backed counterpart reads,
// keyed the same way; document ids are already sanitised where the file name
// would be.
func (w *FirestoreWorkspace) Export(ctx context.Context, sink ExportSink) error {
	for _, export := range []func(context.Context, ExportSink) error{
		w.exportTimeline,
		w.exportSettings,
		w.exportSkills,
		w.exportPersonas,
		w.exportSocialState,
		w.exportPublications,
		w.exportDailyCheckIns,
		w.exportSchedules,
		w.exportWakes,
		w.exportRouting,
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := export(ctx, sink); err != nil {
			return err
		}
	}
	return exportBlobs(ctx, w.blobs, sink)
}

// exportTimeline streams the log as it is read: the timeline is the one part of
// a workspace with no bound, so it is never held as a whole.
func (w *FirestoreWorkspace) exportTimeline(ctx context.Context, sink ExportSink) error {
	return sink.Add(logName, time.Time{}, func(out io.Writer) error {
		iter := w.timeline().OrderBy("At", firestore.Asc).Documents(ctx)
		defer iter.Stop()
		writer := bufio.NewWriter(out)
		for {
			doc, err := iter.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				return err
			}
			var entry timeline.Entry
			if err := doc.DataTo(&entry); err != nil {
				return err
			}
			raw, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			if _, err := writer.Write(append(raw, '\n')); err != nil {
				return err
			}
		}
		return writer.Flush()
	})
}

func (w *FirestoreWorkspace) exportSettings(ctx context.Context, sink ExportSink) error {
	doc, err := w.user.Collection("config").Doc("settings").Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	var settings Settings
	if err := doc.DataTo(&settings); err != nil {
		return err
	}
	return addJSON(sink, "settings.json", time.Time{}, settings)
}

func (w *FirestoreWorkspace) exportSkills(ctx context.Context, sink ExportSink) error {
	iter := w.user.Collection("skills").Documents(ctx)
	defer iter.Stop()
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return err
		}
		raw, _ := doc.Data()["raw"].(string)
		if raw == "" {
			continue
		}
		name := path.Join("skills", sanitise(doc.Ref.ID), "skill.json")
		if err := addBytes(sink, name, time.Time{}, []byte(raw)); err != nil {
			return err
		}
	}
}

// exportPersonas writes each persona's counters and its notes. A note's
// written time is a field in Firestore and a file time on disk, so it travels
// as the file's time: the file store reads it back from there.
func (w *FirestoreWorkspace) exportPersonas(ctx context.Context, sink ExportSink) error {
	iter := w.user.Collection("personas").Documents(ctx)
	defer iter.Stop()
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return err
		}
		var memory Memory
		if err := doc.DataTo(&memory); err != nil {
			return err
		}
		name := sanitise(doc.Ref.ID)
		if err := addJSON(sink, path.Join("memory", name+".meta.json"), time.Time{}, memory); err != nil {
			return err
		}
		if err := w.exportNotes(ctx, sink, doc.Ref.Collection("memory"), name); err != nil {
			return err
		}
	}
}

func (w *FirestoreWorkspace) exportNotes(ctx context.Context, sink ExportSink, notes *firestore.CollectionRef, persona string) error {
	iter := notes.Documents(ctx)
	defer iter.Stop()
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return err
		}
		var piece Note
		if err := doc.DataTo(&piece); err != nil {
			return err
		}
		// The stored path is checked again here: an export writes files, and
		// nothing that came back from a document may name one outside this
		// persona's directory.
		clean, err := notePath(piece.Path)
		if err != nil {
			continue
		}
		if err := addBytes(sink, path.Join("memory", persona, clean), piece.Written, []byte(piece.Text)); err != nil {
			return err
		}
	}
}

// exportSocialState carries the connection data as it stands: encrypted where
// it was encrypted, the same document ids the file store names files by.
func (w *FirestoreWorkspace) exportSocialState(ctx context.Context, sink ExportSink) error {
	docs, err := w.user.Collection("socialState").Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	for _, doc := range docs {
		var value SocialValue
		if err := doc.DataTo(&value); err != nil {
			return err
		}
		if err := addJSON(sink, path.Join("social-state", doc.Ref.ID+".json"), time.Time{}, value); err != nil {
			return err
		}
	}
	return nil
}

func (w *FirestoreWorkspace) exportPublications(ctx context.Context, sink ExportSink) error {
	docs, err := w.user.Collection("publications").Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	for _, doc := range docs {
		var publication Publication
		if err := doc.DataTo(&publication); err != nil {
			return err
		}
		if err := addJSON(sink, path.Join("publications", doc.Ref.ID+".json"), time.Time{}, publication); err != nil {
			return err
		}
	}
	return nil
}

func (w *FirestoreWorkspace) exportDailyCheckIns(ctx context.Context, sink ExportSink) error {
	docs, err := w.user.Collection("dailyCheckIns").Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	days := make(map[string]string, len(docs))
	for _, doc := range docs {
		day, _ := doc.Data()["day"].(string)
		days[doc.Ref.ID] = day
	}
	return addJSON(sink, "daily-check-ins.json", time.Time{}, days)
}

func (w *FirestoreWorkspace) exportSchedules(ctx context.Context, sink ExportSink) error {
	docs, err := w.user.Collection("schedules").Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	schedules := make(map[string]Schedule, len(docs))
	for _, doc := range docs {
		var schedule Schedule
		if err := doc.DataTo(&schedule); err != nil {
			return err
		}
		schedules[doc.Ref.ID] = schedule
	}
	return addJSON(sink, "schedules.json", time.Time{}, schedules)
}

// exportWakes writes the same map the file store reads: an entry id pointing
// at the personas woken for it, without the document wrapper.
func (w *FirestoreWorkspace) exportWakes(ctx context.Context, sink ExportSink) error {
	docs, err := w.user.Collection("wakes").Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	woke := make(map[string][]string, len(docs))
	for _, doc := range docs {
		var record wakeRecord
		if err := doc.DataTo(&record); err != nil {
			return err
		}
		woke[doc.Ref.ID] = record.Skills
	}
	return addJSON(sink, "wakes.json", time.Time{}, woke)
}

func (w *FirestoreWorkspace) exportRouting(ctx context.Context, sink ExportSink) error {
	docs, err := w.user.Collection("routing").Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	records := make(map[string]Routing, len(docs))
	for _, doc := range docs {
		var record Routing
		if err := doc.DataTo(&record); err != nil {
			return err
		}
		records[doc.Ref.ID] = normaliseRouting(record)
	}
	return addJSON(sink, "routing.json", time.Time{}, records)
}

// exportBlobs pulls every capture through the blob store, whether the source is
// disk or object storage, and passes it straight on: one capture is in hand at
// a time and none of them stay.
func exportBlobs(ctx context.Context, blobs BlobStore, sink ExportSink) error {
	if blobs == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	names, err := blobs.List()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !safeName(name) {
			continue
		}
		data, _, err := blobs.Get(name)
		if err != nil {
			return err
		}
		if err := addBytes(sink, path.Join("blobs", name), time.Time{}, data); err != nil {
			return err
		}
	}
	return nil
}

func addBytes(sink ExportSink, name string, modified time.Time, data []byte) error {
	return sink.Add(name, modified, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// addJSON writes a file the way writeJSON writes it on disk, so an exported
// workspace reads the same as one that was always there.
func addJSON(sink ExportSink, name string, modified time.Time, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return addBytes(sink, name, modified, raw)
}
