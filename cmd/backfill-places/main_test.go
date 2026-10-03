package main

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestEligible(t *testing.T) {
	base := timeline.Entry{Kind: timeline.KindPost, Speaker: timeline.User, Place: &timeline.Place{Lat: 0, Lon: 0}}
	if !eligible(base) {
		t.Fatal("zero coordinates must be eligible")
	}
	for _, modify := range []func(*timeline.Entry){
		func(e *timeline.Entry) { e.Deleted = true },
		func(e *timeline.Entry) { e.Kind = timeline.KindReply },
		func(e *timeline.Entry) { e.Speaker = "persona" },
		func(e *timeline.Entry) { e.Place = nil },
		func(e *timeline.Entry) { e.Place = &timeline.Place{Label: "already named"} },
		func(e *timeline.Entry) { e.Place = &timeline.Place{Lat: 91} },
		func(e *timeline.Entry) { e.Place = &timeline.Place{Lon: math.Inf(1)} },
		func(e *timeline.Entry) { e.Place = &timeline.Place{Lat: math.NaN()} },
	} {
		e := base
		modify(&e)
		if eligible(e) {
			t.Fatalf("unexpected candidate: %+v", e)
		}
	}
}

func TestResolved(t *testing.T) {
	before := timeline.Place{Lat: 35, Lon: 139}
	after, found, err := resolved(json.RawMessage(`{"found":true,"place":{"name":"Tokyo","countryCode":"JP","geonameId":1850147}}`), before)
	if err != nil || !found || after.Label != "Tokyo（JP）" || !after.Approximate || after.GeonameID != 1850147 || after.Lat != before.Lat || after.Lon != before.Lon {
		t.Fatalf("%+v %v %v", after, found, err)
	}
	unchanged, found, err := resolved(json.RawMessage(`{"found":false}`), before)
	if err != nil || found || unchanged != before {
		t.Fatal("no-match must retain coordinates")
	}
	for _, raw := range []string{`{`, `{}`, `null`, `{"found":true}`, `{"found":true,"place":{"name":" "}}`} {
		if _, _, err := resolved(json.RawMessage(raw), before); err == nil {
			t.Fatalf("accepted malformed result: %s", raw)
		}
	}
}

func TestPlanValidation(t *testing.T) {
	base := change{Path: "users/u/timeline/p", Updated: time.Now(), Before: timeline.Place{Lat: 35, Lon: 139}, After: timeline.Place{Lat: 35, Lon: 139, Label: "Tokyo", Approximate: true}}
	if !validChange(base) {
		t.Fatal("valid change refused")
	}
	for _, modify := range []func(*change){
		func(c *change) { c.Path = "users/u/settings/main" },
		func(c *change) { c.Path = "users//timeline/p" },
		func(c *change) { c.Updated = time.Time{} },
		func(c *change) { c.Before.Label = "existing" },
		func(c *change) { c.After.Lat++ },
		func(c *change) { c.After.Label = "" },
	} {
		c := base
		modify(&c)
		if validChange(c) {
			t.Fatalf("accepted unsafe change: %+v", c)
		}
	}
}
