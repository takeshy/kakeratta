package search

import (
	"context"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// IndexPending runs after writes and image analysis, independently of searches.
// Persistent vectors/dirty flags also let searches or a restarted worker resume.
func (s *Service) IndexPending(ctx context.Context) error {
	settings := s.workspace.Settings()
	if settings.APIKey == "" {
		return nil
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	if index, ok := s.workspace.(nativeIndex); ok {
		modelKey := contentHash(settings.BaseURL + "\x00" + Model + "\x001024\x00passages-v1")
		return s.indexNative(ctx, index, settings, modelKey)
	}
	entries, err := store.SearchTimeline(ctx, s.workspace)
	if err != nil {
		return err
	}
	roots := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind == timeline.KindPost && !entry.Deleted {
			roots[entry.ID] = true
		}
	}
	var candidates []timeline.Entry
	for _, entry := range entries {
		if !entry.Deleted && roots[timeline.RootOf(entry)] && Text(entry) != "" {
			candidates = append(candidates, entry)
		}
	}
	_, _, err = s.prepareLocal(ctx, candidates, settings)
	return err
}
