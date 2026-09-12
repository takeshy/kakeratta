package speech

import (
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
)

func TestStoredSpeechKeyReplacesBrowserMask(t *testing.T) {
	for _, tc := range []struct{ endpoint, base, target, header, value string }{
		{EndpointOpenAI, "https://speech.example/v1", "https://speech.example/v1/audio/transcriptions", "Authorization", "Bearer secret"},
		{EndpointGemini, "", "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-transcribe:generateContent", "x-goog-api-key", "secret"},
		{"azure-mai-transcribe", "https://speech.example", "https://speech.example/speechtotext/transcriptions:transcribe?api-version=2025-10-15", "Ocp-Apim-Subscription-Key", "secret"},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			settings := store.SpeechSettings{EndpointType: tc.endpoint, BaseURL: tc.base, APIKey: "secret"}
			request := ExternalHTTPRequest{URL: tc.target, Method: "POST", Headers: map[string]string{tc.header: "********", "Content-Type": "application/json"}}
			prepared, err := prepareSpeechRequest(settings, request)
			if err != nil || prepared.Headers[tc.header] != tc.value || prepared.Headers["Content-Type"] != "application/json" {
				t.Fatalf("prepared = %+v, err = %v", prepared, err)
			}
			for _, target := range []string{"https://attacker.example/audio/transcriptions", tc.target + "/other", tc.target + "#fragment"} {
				request.URL = target
				if _, err := prepareSpeechRequest(settings, request); err == nil {
					t.Fatalf("accepted %s", target)
				}
			}
		})
	}
}
