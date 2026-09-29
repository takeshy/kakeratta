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
	// Tracked says the two lists below are this record's own account of the
	// turns, so an empty one means "nobody yet" rather than "not kept".
	//
	// The lists cannot carry that themselves. Firestore reads an empty array
	// back as a nil slice, so a record written the moment the router decided -
	// every turn still ahead of it - came back indistinguishable from one
	// written before turns were kept at all, and every persona read as
	// finished and silent while it was still being asked.
	Tracked bool `json:"tracked"`
	// Done lists the personas whose turn on this post has finished, spoken or
	// not, so a reader can tell "still thinking" from "stayed quiet". Only
	// meaningful on a tracked record.
	Done []string `json:"done"`
	// Spoke lists the personas that produced a remark during this turn. On an
	// untracked record the speech outcome must be inferred from the timeline.
	Spoke []string `json:"spoke"`
	// Heard is, for a person delivered by a name or keyword rather than by the
	// router, the word it was found by.
	Heard map[string]string `json:"heard,omitempty"`
	// Called is, for a persona delivered by the user's own doing rather than by
	// the router, how: "mention" for an @, "reply" for a reply under its remark
	// or post. Such a persona is also listed in Delivered.
	Called map[string]string `json:"called,omitempty"`
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
	out := Routing{Delivered: []string{}, Excluded: map[string]string{}, Tracked: record.Tracked}
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
	if record.Spoke != nil {
		out.Spoke = append([]string{}, record.Spoke...)
	}
	for id, term := range record.Heard {
		if out.Heard == nil {
			out.Heard = map[string]string{}
		}
		out.Heard[id] = term
	}
	for id, how := range record.Called {
		if out.Called == nil {
			out.Called = map[string]string{}
		}
		out.Called[id] = how
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
