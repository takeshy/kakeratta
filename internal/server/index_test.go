package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
)

func TestPostAndCompletedImageAreIndexedWithoutSearching(t *testing.T) {
	s, w := app(t)
	indexed := make(chan string, 16)
	analyze := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "chat/completions") {
			select {
			case <-analyze:
			case <-r.Context().Done():
				return
			}
			json.NewEncoder(rw).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"text":"おすすめ","describe":"サザエの写真","origin":"食事"}`}}}})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "embeddings") {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		var body struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&body)
		var rows []any
		for i, text := range body.Input {
			indexed <- text
			v := make([]float64, search.Dimensions)
			v[0] = 1
			rows = append(rows, map[string]any{"index": i, "embedding": v})
		}
		json.NewEncoder(rw).Encode(map[string]any{"data": rows})
	}))
	defer provider.Close()
	// Release analysis before closing the server if an assertion fails.
	defer func() {
		select {
		case <-analyze:
		default:
			close(analyze)
		}
		s.Close()
	}()
	w.SaveSettings(store.Settings{Provider: store.ProviderOpenAI, APIKey: "test", BaseURL: provider.URL, VisionModel: "vision", Active: []string{}})
	raw, _ := json.Marshal(postBody{Text: "夕食", Image: "data:image/png;base64,AQ=="})
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.ServeHTTP(response, httptest.NewRequest("POST", "/api/post", bytes.NewReader(raw)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("post waited for image analysis/indexing")
	}
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	select {
	case text := <-indexed:
		if text != "夕食" {
			t.Fatal(text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("saved post was not indexed")
	}
	close(analyze)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case text := <-indexed:
			if strings.Contains(text, "サザエの写真") && strings.Contains(text, "おすすめ") {
				return
			}
		case <-deadline:
			t.Fatal("completed image was not indexed")
		}
	}
}
