package store

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestPublicationClaimsAndPersistence(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var won atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.ClaimPublication("bluesky:account:entry", Publication{State: "pending"})
			if err != nil {
				t.Error(err)
			}
			if ok {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatal(won.Load())
	}
	if err := s.SavePublication("bluesky:account:entry", Publication{State: "posted", URI: "uri", CID: "cid"}); err != nil {
		t.Fatal(err)
	}
	reopened, _ := Open(s.Root())
	p, err := reopened.Publication("bluesky:account:entry")
	if err != nil || p.CID != "cid" {
		t.Fatalf("%+v %v", p, err)
	}
	if ok, _ := reopened.ClaimPublication("bluesky:other-account:entry", Publication{State: "pending"}); !ok {
		t.Fatal("account isolation")
	}
}
