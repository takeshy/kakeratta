package capture

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
)

func TestReadLanguagePreservesOriginalText(t *testing.T) {
	for _, language := range []string{"", "ja", "en"} {
		t.Run(language, func(t *testing.T) {
			var prompt string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []struct {
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if len(request.Messages) > 0 {
					_ = json.Unmarshal(request.Messages[0].Content, &prompt)
				}
				content, _ := json.Marshal(map[string]string{"text": "日本のワイン", "describe": "A wine bottle.", "origin": "ALPS WINE"})
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}})
			}))
			defer server.Close()
			media := Read(context.Background(), llm.New(server.URL, ""), "vision", []byte("image"), "image/jpeg", language)
			if media.Error != "" || media.Text != "日本のワイン" || media.Describe != "A wine bottle." {
				t.Fatalf("media: %+v", media)
			}
			if strings.Contains(prompt, "Write describe and origin in English") != (language == "en") {
				t.Fatalf("wrong language instruction for %q", language)
			}
			if language == "en" && !strings.Contains(prompt, "do not translate the OCR text") {
				t.Fatal("OCR must preserve the source language")
			}
		})
	}
}
