package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// What the router decided about a post can be read back, named the way the
// personas are named today. A post the router never answered for reads as
// unrecorded, and a deleted post takes its record with it.
func TestRoutingDecisionCanBeRead(t *testing.T) {
	server, workspace := app(t)
	get := func(id string) (int, map[string]any) {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/entry/"+id+"/routing", nil))
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return response.Code, body
	}
	if code, _ := get("missing"); code != http.StatusNotFound {
		t.Fatalf("unknown post: %d", code)
	}
	for _, id := range []string{"answered", "unanswered"} {
		if err := workspace.Append(timeline.Entry{ID: id, Kind: timeline.KindPost, Speaker: timeline.User, Text: "ラーメン", At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if code, body := get("unanswered"); code != http.StatusOK || body["recorded"] != false {
		t.Fatalf("no record: %d %v", code, body)
	}
	if err := workspace.SaveRouting("answered", store.Routing{
		Delivered: []string{"gourmet"},
		Excluded:  map[string]string{"move": "移動の話ではない", "book": "本の話ではない", "gone": "消えた担当"},
	}); err != nil {
		t.Fatal(err)
	}
	code, body := get("answered")
	if code != http.StatusOK || body["recorded"] != true {
		t.Fatalf("record: %d %v", code, body)
	}
	delivered := body["delivered"].([]any)
	if len(delivered) != 1 || delivered[0].(map[string]any)["name"] != "tabe" || delivered[0].(map[string]any)["icon"] == "" {
		t.Fatalf("delivered = %v", delivered)
	}
	excluded := body["excluded"].([]any)
	var ids, reasons, names []string
	for _, row := range excluded {
		ids = append(ids, row.(map[string]any)["id"].(string))
		reasons = append(reasons, row.(map[string]any)["reason"].(string))
		names = append(names, row.(map[string]any)["name"].(string))
	}
	if strings.Join(ids, ",") != "book,gone,move" {
		t.Fatalf("excluded not in a fixed order: %v", ids)
	}
	if reasons[0] != "本の話ではない" || names[0] != "shiori" || names[1] != "gone" || names[2] != "michi" {
		t.Fatalf("rows = %v %v", names, reasons)
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/entry/answered", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("delete: %d", response.Code)
	}
	if record, err := workspace.Routing("answered"); err != nil || record != nil {
		t.Fatalf("deleted post kept its routing record: %v (%v)", record, err)
	}
}

// After changing a persona's conditions, a person can have the router try the
// same post again and see whether it now lands where they meant. Nothing is
// delivered and nothing is written down.
func TestRoutingCheckTriesThePostAgain(t *testing.T) {
	server, workspace := app(t)
	var reply string
	var conditions string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		conditions = request.Messages[len(request.Messages)-1].Content
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": reply}}}})
	}))
	defer model.Close()
	settings := workspace.ConfiguredSettings()
	settings.BaseURL = model.URL
	settings.Active = []string{"gourmet", "book"}
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Append(timeline.Entry{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Text: "ラーメンの本を読んだ", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	check := func(id, body string) (int, map[string]any) {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/entry/"+id+"/routing/check", strings.NewReader(body)))
		var out map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return response.Code, out
	}
	reply = `{"deliver":["gourmet"],"excluded_reasons":{"book":"本の感想ではない"}}`
	if code, out := check("p", `{"persona":"gourmet"}`); code != http.StatusOK || out["answered"] != true || out["delivered"] != true {
		t.Fatalf("delivered: %d %v", code, out)
	}
	if !strings.Contains(conditions, "ラーメンの本を読んだ") || !strings.Contains(conditions, "shiori") {
		t.Fatalf("the router was not given the post and the active conditions: %s", conditions)
	}
	if code, out := check("p", `{"persona":"book"}`); code != http.StatusOK || out["delivered"] != false || out["reason"] != "本の感想ではない" {
		t.Fatalf("passed over: %d %v", code, out)
	}
	reply = `壊れた返事`
	if code, out := check("p", `{"persona":"book"}`); code != http.StatusOK || out["answered"] != false || out["error"] == "" {
		t.Fatalf("no answer: %d %v", code, out)
	}
	if code, _ := check("p", `{"persona":"move"}`); code != http.StatusBadRequest {
		t.Fatalf("inactive persona: %d", code)
	}
	if code, _ := check("p", `{}`); code != http.StatusBadRequest {
		t.Fatalf("no persona: %d", code)
	}
	if code, _ := check("missing", `{"persona":"gourmet"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown post: %d", code)
	}
	if record, _ := workspace.Routing("p"); record != nil {
		t.Fatal("a trial run wrote a record")
	}
	if woke, _ := workspace.Woke("p"); len(woke) != 0 {
		t.Fatal("a trial run woke someone")
	}
}

// A persona's turn can be previewed on the remark it made, but only for that
// persona's own remark, and only signed in.
func TestReplyPreviewIsGuarded(t *testing.T) {
	server, workspace := app(t)
	now := time.Now()
	for _, entry := range []timeline.Entry{
		{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Text: "渋谷に行きたい", At: now},
		{ID: "r", Kind: timeline.KindRemark, Speaker: "move", Root: "p", ReplyTo: "p", Text: "渋谷ですね", At: now.Add(time.Minute)},
	} {
		if err := workspace.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	preview := func(id, body string) int {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/entry/"+id+"/reply/preview", strings.NewReader(body)))
		return response.Code
	}
	for _, tc := range []struct{ id, body string }{
		{"p", `{"persona":"move"}`},    // not a remark
		{"r", `{"persona":"gourmet"}`}, // somebody else's remark
		{"r", `{}`},                    // no persona
		{"missing", `{"persona":"move"}`},
	} {
		if code := preview(tc.id, tc.body); code != http.StatusBadRequest {
			t.Fatalf("%s %s: %d", tc.id, tc.body, code)
		}
	}

}

func TestRoutingDecisionMustMatchCurrentText(t *testing.T) {
	server, workspace := app(t)
	old := "old text"
	if err := workspace.Append(timeline.Entry{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Text: "edited text", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// An old in-flight result can finish after the edit invalidates its record.
	if err := workspace.SaveRouting("p", store.Routing{Delivered: []string{"book"}, EntryText: &old}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/entry/p/routing", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"recorded":false`) {
		t.Fatalf("stale decision exposed: %d %s", response.Code, response.Body.String())
	}
}

// For a persona the router chose, the sheet needs to know whether its turn
// has finished and whether it said anything - "still working" and "stayed
// quiet" look the same on the timeline and are not the same thing.
func TestRoutingSaysWhoFinishedAndWhoSpoke(t *testing.T) {
	server, workspace := app(t)
	now := time.Now()
	for _, entry := range []timeline.Entry{
		{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Text: "ラーメン", At: now},
		{ID: "r", Kind: timeline.KindRemark, Speaker: "gourmet", Root: "p", ReplyTo: "p", Text: "いいね", At: now.Add(time.Second)},
	} {
		if err := workspace.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]map[string]any {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/entry/p/routing", nil))
		var body struct{ Delivered []map[string]any }
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		rows := map[string]map[string]any{}
		for _, row := range body.Delivered {
			rows[row["id"].(string)] = row
		}
		return rows
	}
	// Turns under way: gourmet has spoken, move and book have not finished.
	if err := workspace.SaveRouting("p", store.Routing{Delivered: []string{"gourmet", "move", "book"}, Excluded: map[string]string{}, Tracked: true, Done: []string{"gourmet"}, Spoke: []string{"gourmet"}}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.WriteNote("gourmet", "shops/ramen.md", "type: Shop\n\nラーメン", store.NoteSource{EntryID: "p", Label: "ラーメン"}); err != nil {
		t.Fatal(err)
	}
	rows := read()
	if rows["gourmet"]["done"] != true || rows["gourmet"]["spoke"] != true || rows["gourmet"]["noted"] != true {
		t.Fatalf("gourmet = %v", rows["gourmet"])
	}
	if notes, ok := rows["gourmet"]["notes"].([]any); !ok || len(notes) != 1 || notes[0] != "shops/ramen.md" {
		t.Fatalf("gourmet note links = %v", rows["gourmet"]["notes"])
	}
	if rows["move"]["noted"] != false {
		t.Fatalf("a persona without a note on this post = %v", rows["move"])
	}
	if rows["move"]["done"] != false || rows["move"]["spoke"] != false {
		t.Fatalf("a persona still working reads as finished: %v", rows["move"])
	}
	// A stored reply outcome is authoritative even when the timeline cannot
	// identify it by timestamp (for example, both entries share the same time).
	if err := workspace.SaveRouting("p", store.Routing{Delivered: []string{"gourmet", "move", "book"}, Excluded: map[string]string{}, Tracked: true, Done: []string{"gourmet", "move"}, Spoke: []string{"gourmet", "move"}}); err != nil {
		t.Fatal(err)
	}
	if rows = read(); rows["move"]["spoke"] != true {
		t.Fatalf("stored speech outcome was ignored: %v", rows["move"])
	}
	// move finished without a word.
	if err := workspace.SaveRouting("p", store.Routing{Delivered: []string{"gourmet", "move", "book"}, Excluded: map[string]string{}, Tracked: true, Done: []string{"gourmet", "move"}}); err != nil {
		t.Fatal(err)
	}
	rows = read()
	if rows["move"]["done"] != true || rows["move"]["spoke"] != false {
		t.Fatalf("a quiet persona = %v", rows["move"])
	}
	// A record from before turns were noted reads as finished.
	if err := workspace.SaveRouting("p", store.Routing{Delivered: []string{"book"}, Excluded: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if rows = read(); rows["book"]["done"] != true {
		t.Fatalf("an old record = %v", rows["book"])
	}
}

// The moment the router decides, nobody has finished yet - and the record says
// so by being tracked with empty lists, not by the lists themselves, which is
// the one thing they cannot say. Firestore hands an empty array back as a nil
// slice, so a record whose turns are all still ahead of it arrives looking
// exactly like one written before turns were kept. Reading finished off that
// told a person every persona had been asked and had stayed silent, while they
// were in fact still being asked.
func TestRoutingWithNoTurnsYetIsNotReadAsFinished(t *testing.T) {
	server, workspace := app(t)
	if err := workspace.Append(timeline.Entry{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Text: "ラーメン", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// Exactly what Firestore returns for the record keepRouting writes: tracked,
	// with both lists empty and so nil.
	if err := workspace.SaveRouting("p", store.Routing{
		Delivered: []string{"gourmet", "book"}, Excluded: map[string]string{}, Tracked: true, Done: nil, Spoke: nil,
	}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/entry/p/routing", nil))
	var body struct{ Delivered []map[string]any }
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if len(body.Delivered) != 2 {
		t.Fatalf("delivered = %v", body.Delivered)
	}
	for _, row := range body.Delivered {
		if row["done"] != false || row["spoke"] != false {
			t.Fatalf("a persona still being asked reads as finished: %v", row)
		}
	}
}

// A persona the user called by @ or by replying is listed with how, next to
// the router's own choices, and its notes are reachable like theirs.
func TestRoutingSaysHowAPersonaWasCalled(t *testing.T) {
	server, workspace := app(t)
	if err := workspace.Append(timeline.Entry{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Text: "@tabe ラーメン", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveRouting("p", store.Routing{
		Delivered: []string{"move", "gourmet", "book"}, Excluded: map[string]string{}, Tracked: true, Done: []string{}, Spoke: []string{},
		Called: map[string]string{"gourmet": "mention", "book": "reply"},
	}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/entry/p/routing", nil))
	var body struct{ Delivered []map[string]any }
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	via := map[string]any{}
	for _, row := range body.Delivered {
		via[row["id"].(string)] = row["via"]
	}
	if len(body.Delivered) != 3 || via["gourmet"] != "mention" || via["book"] != "reply" || via["move"] != nil {
		t.Fatalf("delivered = %v", body.Delivered)
	}
}
