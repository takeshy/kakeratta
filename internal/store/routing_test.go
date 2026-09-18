package store

import (
	"reflect"
	"testing"
)

// The router's decision is kept per post so a person can see who it chose and
// why it passed over the rest. A post routed again has one decision, and a
// deleted post has none.
func TestRoutingRecordRoundTrip(t *testing.T) {
	s := open(t)
	if record, err := s.Routing("e_none"); err != nil || record != nil {
		t.Fatalf("unknown post = %v (%v)", record, err)
	}
	first := Routing{Delivered: []string{"gourmet"}, Excluded: map[string]string{"book": "not about books"}}
	if err := s.SaveRouting("e_1", first); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRouting("e_2", Routing{}); err != nil {
		t.Fatal(err)
	}
	record, err := s.Routing("e_1")
	if err != nil || record == nil || !reflect.DeepEqual(*record, first) {
		t.Fatalf("record = %+v (%v)", record, err)
	}
	// Two readers never share a map.
	record.Excluded["routine"] = "changed by a reader"
	if again, _ := s.Routing("e_1"); len(again.Excluded) != 1 {
		t.Fatalf("a reader changed the stored record: %+v", again)
	}

	second := Routing{Delivered: []string{}, Excluded: map[string]string{"gourmet": "no longer food", "book": "still not books"}}
	if err := s.SaveRouting("e_1", second); err != nil {
		t.Fatal(err)
	}
	if record, _ := s.Routing("e_1"); !reflect.DeepEqual(*record, second) {
		t.Fatalf("routing again did not replace the record: %+v", record)
	}

	empty, err := s.Routing("e_2")
	if err != nil || empty == nil || len(empty.Delivered) != 0 || len(empty.Excluded) != 0 {
		t.Fatalf("an answer with nobody in it is still an answer: %+v (%v)", empty, err)
	}

	if err := s.ForgetRouting("e_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetRouting("e_never"); err != nil {
		t.Fatalf("forgetting nothing failed: %v", err)
	}
	if record, err := s.Routing("e_1"); err != nil || record != nil {
		t.Fatalf("after forgetting = %v (%v)", record, err)
	}
	if record, err := s.Routing("e_2"); err != nil || record == nil {
		t.Fatalf("another post was cleared: %v (%v)", record, err)
	}
}
