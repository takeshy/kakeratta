package store

import "testing"

// The app remembers who it woke so that a deleted post can reach them. It is
// the only record of that, and it is spent once the deletion is handled.
func TestWakeRecordRoundTrip(t *testing.T) {
	s := open(t)
	if woke, err := s.Woke("e_none"); err != nil || len(woke) != 0 {
		t.Fatalf("unknown post = %v (%v)", woke, err)
	}
	for _, skill := range []string{"gourmet", "routine", "gourmet"} {
		if err := s.RecordWake("e_1", skill); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordWake("e_2", "book"); err != nil {
		t.Fatal(err)
	}
	woke, err := s.Woke("e_1")
	if err != nil || len(woke) != 2 || woke[0] != "gourmet" || woke[1] != "routine" {
		t.Fatalf("woke = %v (%v)", woke, err)
	}
	if err := s.ForgetWakes("e_1"); err != nil {
		t.Fatal(err)
	}
	if woke, err := s.Woke("e_1"); err != nil || len(woke) != 0 {
		t.Fatalf("after forgetting = %v (%v)", woke, err)
	}
	if woke, err := s.Woke("e_2"); err != nil || len(woke) != 1 {
		t.Fatalf("another post was cleared: %v (%v)", woke, err)
	}
}
