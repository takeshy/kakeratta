package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestReplyPersistsDisplayLanguage(t *testing.T) {
	s, st := app(t)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, posted(t, "記事を読んだ"))
	var parent timeline.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &parent); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"ja", "en", "invalid"} {
		body, _ := json.Marshal(replyBody{ReplyTo: parent.ID, Text: "インフォグラフィックにして", Language: language})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/api/reply", bytes.NewReader(body)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var reply timeline.Entry
		if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		stored, ok := st.Entry(reply.ID)
		if !ok || stored.Language != displayLanguage(language) {
			t.Fatalf("stored reply = %+v", stored)
		}
	}
}
