package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NoteJob keeps a completed turn's note work until both its notes and index
// have been saved. Payload is interpreted by the persona runner.
type NoteJob struct {
	ID          string    `json:"id" firestore:"id"`
	Persona     string    `json:"persona" firestore:"persona"`
	Stage       string    `json:"stage" firestore:"stage"`
	Payload     []byte    `json:"payload" firestore:"payload"`
	Attempts    int       `json:"attempts" firestore:"attempts"`
	NextAttempt time.Time `json:"nextAttempt" firestore:"nextAttempt"`
	LastError   string    `json:"lastError,omitempty" firestore:"lastError"`
}

func (s *Store) NoteJobs() ([]NoteJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	paths, err := filepath.Glob(s.path("note-jobs", "*.json"))
	if err != nil {
		return nil, err
	}
	var jobs []NoteJob
	for _, path := range paths {
		var job NoteJob
		if err := readJSON(path, &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *Store) SaveNoteJob(job NoteJob) error {
	if job.ID == "" || job.Persona == "" {
		return errors.New("note job needs id and persona")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.path("note-jobs", publicationKey(job.ID)+".json"), job)
}

func (s *Store) DeleteNoteJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path("note-jobs", publicationKey(id)+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (w *FirestoreWorkspace) NoteJobs() ([]NoteJob, error) {
	iter := w.user.Collection("noteJobs").Documents(context.Background())
	defer iter.Stop()
	var jobs []NoteJob
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var job NoteJob
		if err := doc.DataTo(&job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs, nil
}

func (w *FirestoreWorkspace) SaveNoteJob(job NoteJob) error {
	if job.ID == "" || job.Persona == "" {
		return errors.New("note job needs id and persona")
	}
	_, err := w.user.Collection("noteJobs").Doc(publicationKey(job.ID)).Set(context.Background(), job)
	return err
}

func (w *FirestoreWorkspace) DeleteNoteJob(id string) error {
	_, err := w.user.Collection("noteJobs").Doc(publicationKey(id)).Delete(context.Background())
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}
