package store

import (
	"context"
	"os"
	"path/filepath"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Routing is what the router decided about one post, kept so a person can see
// it afterwards: who the router chose, and for whom it said no and why. A
// persona reached by name is not in it, since that was not the router's call.
type Routing struct {
	Delivered []string          `json:"delivered"`
	Excluded  map[string]string `json:"excluded"`
	EntryText *string           `json:"entryText,omitempty"`
	// Done lists the personas whose turn on this post has finished, spoken or
	// not, so a reader can tell "still thinking" from "stayed quiet". nil is a
	// record from before this was kept, and reads as finished.
	Done []string `json:"done"`
}

func routingPath(root string) string { return filepath.Join(root, "routing.json") }

func (s *Store) readRouting() (map[string]Routing, error) {
	records := map[string]Routing{}
	if err := readJSON(routingPath(s.root), &records); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return records, nil
}

// SaveRouting keeps the router's decision about one post, replacing any
// earlier one - a post routed again after an edit has one current decision.
func (s *Store) SaveRouting(entryID string, record Routing) error {
	if entryID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.readRouting()
	if err != nil {
		return err
	}
	records[entryID] = normaliseRouting(record)
	return writeJSON(routingPath(s.root), records)
}

// Routing returns the decision kept for one post, or nil when the router never
// answered for it.
func (s *Store) Routing(entryID string) (*Routing, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	records, err := s.readRouting()
	if err != nil {
		return nil, err
	}
	record, ok := records[entryID]
	if !ok {
		return nil, nil
	}
	copied := normaliseRouting(record)
	return &copied, nil
}

// ForgetRouting drops the decision for one post, when the post itself goes.
func (s *Store) ForgetRouting(entryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.readRouting()
	if err != nil {
		return err
	}
	if _, ok := records[entryID]; !ok {
		return nil
	}
	delete(records, entryID)
	return writeJSON(routingPath(s.root), records)
}

// normaliseRouting copies a record so callers never share slices or maps, and
// so an empty decision is written as empty rather than null.
func normaliseRouting(record Routing) Routing {
	out := Routing{Delivered: []string{}, Excluded: map[string]string{}}
	if record.EntryText != nil {
		text := *record.EntryText
		out.EntryText = &text
	}
	out.Delivered = append(out.Delivered, record.Delivered...)
	for id, reason := range record.Excluded {
		out.Excluded[id] = reason
	}
	if record.Done != nil {
		out.Done = append([]string{}, record.Done...)
	}
	return out
}

func (w *FirestoreWorkspace) SaveRouting(entryID string, record Routing) error {
	if entryID == "" {
		return nil
	}
	_, err := w.user.Collection("routing").Doc(sanitise(entryID)).Set(context.Background(), normaliseRouting(record))
	return err
}

func (w *FirestoreWorkspace) Routing(entryID string) (*Routing, error) {
	doc, err := w.user.Collection("routing").Doc(sanitise(entryID)).Get(context.Background())
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}
	var record Routing
	if err := doc.DataTo(&record); err != nil {
		return nil, err
	}
	record = normaliseRouting(record)
	return &record, nil
}

func (w *FirestoreWorkspace) ForgetRouting(entryID string) error {
	_, err := w.user.Collection("routing").Doc(sanitise(entryID)).Delete(context.Background())
	return err
}
