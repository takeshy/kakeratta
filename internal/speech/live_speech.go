package speech

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/takeshy/dokking/internal/store"
)

const liveSpeechEvent = "speech:live"

type liveSpeechEventData struct {
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
	ItemID    string `json:"itemId,omitempty"`
	Text      string `json:"text,omitempty"`
	Message   string `json:"message,omitempty"`
}

type liveSpeechSession struct {
	id        string
	provider  string
	conn      *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	writeMu   sync.Mutex
	done      chan struct{}
	finishing atomic.Bool
}

func (s *liveSpeechSession) write(payload any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	return wsjsonWrite(ctx, s.conn, payload)
}

// StartLiveSpeech opens the provider WebSocket on the Go side so standard API
// keys never appear in a WebSocket URL or browser developer tools.
// How long a provider may take to send the closing transcript after the audio
// stream ends before the session is abandoned.
const liveSpeechFinalizeTimeout = 10 * time.Second

func (v *Service) StartLiveSpeech() (string, error) {
	v.StopLiveSpeech()

	speech := v.settings()
	if speech.Provider != ProviderLive {
		return "", errors.New("live speech is not selected")
	}
	if speech.EndpointType != EndpointOpenAI && speech.EndpointType != EndpointGemini && speech.EndpointType != EndpointVertex {
		return "", errors.New("live speech supports OpenAI, Gemini API, and Vertex AI only")
	}
	if speech.EndpointType != EndpointVertex && strings.TrimSpace(speech.APIKey) == "" {
		return "", errors.New("API key is required")
	}

	parent := v.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	id := fmt.Sprintf("live-%d", time.Now().UnixNano())
	s := &liveSpeechSession{id: id, provider: speech.EndpointType, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	headers := http.Header{}
	var endpoint string
	if speech.EndpointType == EndpointOpenAI {
		endpoint = "wss://api.openai.com/v1/realtime?intent=transcription"
		headers.Set("Authorization", "Bearer "+speech.APIKey)
	} else if speech.EndpointType == EndpointGemini {
		endpoint = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
		headers.Set("x-goog-api-key", speech.APIKey)
	} else {
		token, err := v.vertexOAuthAccessToken()
		if err != nil {
			cancel()
			return "", err
		}
		endpoint = "wss://aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
		headers.Set("Authorization", "Bearer "+token)
	}
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: headers, HTTPClient: &http.Client{Transport: publicNetworkTransport()}})
	if err != nil {
		cancel()
		return "", fmt.Errorf("live speech connection failed: %w", err)
	}
	s.conn = conn
	if err := s.write(liveSpeechSetup(speech)); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "setup failed")
		cancel()
		return "", fmt.Errorf("live speech setup failed: %w", err)
	}
	if err := waitLiveSpeechReady(s); err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "setup rejected")
		cancel()
		return "", fmt.Errorf("live speech setup failed: %w", err)
	}

	v.liveMu.Lock()
	v.liveSpeech = s
	v.liveMu.Unlock()
	go v.readLiveSpeech(s)
	return id, nil
}

// Providers require setup acknowledgement before accepting audio. Waiting here
// also turns an asynchronous setup rejection into a useful start error.
func waitLiveSpeechReady(s *liveSpeechSession) error {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	for {
		_, data, err := s.conn.Read(ctx)
		if err != nil {
			return err
		}
		var message map[string]any
		if err := json.Unmarshal(data, &message); err != nil {
			return errors.New("invalid live speech setup response")
		}
		if remote, ok := message["error"].(map[string]any); ok {
			return errors.New(liveProviderError(s.provider, remote))
		}
		if s.provider == EndpointOpenAI {
			kind, _ := message["type"].(string)
			if kind == "session.updated" {
				return nil
			}
		} else if message["setupComplete"] != nil || message["setup_complete"] != nil {
			return nil
		}
	}
}

func liveSpeechSetup(speech store.SpeechSettings) any {
	language := strings.TrimSpace(speech.Language)
	if strings.EqualFold(language, "auto") {
		language = ""
	}
	if speech.EndpointType == EndpointOpenAI {
		transcription := map[string]any{"model": "gpt-live-transcribe", "delay": "low"}
		if language != "" {
			transcription["languages"] = []string{language}
		}
		return map[string]any{"type": "session.update", "session": map[string]any{
			"type": "transcription",
			"audio": map[string]any{"input": map[string]any{
				"format":        map[string]any{"type": "audio/pcm", "rate": 24000},
				"transcription": transcription,
				// gpt-live-transcribe currently rejects turn detection. The client
				// commits the input buffer explicitly when recording stops.
				"turn_detection": nil,
			}},
		}}
	}
	languages := []string{}
	if language != "" {
		languages = append(languages, language)
	}
	model := "models/gemini-3.5-transcribe-live"
	if speech.EndpointType == EndpointVertex {
		model = fmt.Sprintf("projects/%s/locations/global/publishers/google/models/gemini-3.5-transcribe-live-preview", speech.VertexProjectID)
	}
	return map[string]any{"setup": map[string]any{
		"model":                   model,
		"generationConfig":        map[string]any{"responseModalities": []string{"TEXT"}},
		"inputAudioTranscription": map[string]any{"languageCodes": languages, "mode": "SMART"},
	}}
}

// SendLiveSpeechAudio forwards one base64 encoded raw PCM16 chunk.
func (v *Service) SendLiveSpeechAudio(sessionID, audio string) error {
	s, err := v.currentLiveSpeech(sessionID)
	if err != nil {
		return err
	}
	if len(audio) == 0 || len(audio) > 1024*1024 {
		return errors.New("invalid live audio chunk")
	}
	if s.provider == EndpointOpenAI {
		return s.write(map[string]any{"type": "input_audio_buffer.append", "audio": audio})
	}
	return s.write(map[string]any{"realtimeInput": map[string]any{"audio": map[string]any{
		"data": audio, "mimeType": "audio/pcm;rate=16000",
	}}})
}

// FinishLiveSpeech flushes the last provider turn. The read loop remains alive
// briefly so the authoritative final transcript can arrive.
func (v *Service) FinishLiveSpeech(sessionID string) error {
	s, err := v.currentLiveSpeech(sessionID)
	if err != nil {
		return err
	}
	s.finishing.Store(true)
	var payload any
	if s.provider == EndpointOpenAI {
		// Turn detection is disabled for gpt-live-transcribe, so stopping the
		// recording is the one authoritative boundary for this audio turn.
		payload = map[string]any{"type": "input_audio_buffer.commit"}
	} else {
		payload = map[string]any{"realtimeInput": map[string]any{"audioStreamEnd": true}}
	}
	if err := s.write(payload); err != nil {
		return err
	}
	go func() {
		select {
		case <-s.done:
		case <-time.After(liveSpeechFinalizeTimeout):
			// The closing transcript never arrived, so the draft is missing its
			// tail: report that rather than a normal completion.
			v.emitLiveSpeech(liveSpeechEventData{
				SessionID: s.id, Kind: "error",
				Message: "ライブ文字起こしの終了が間に合いませんでした。末尾が欠けている可能性があります",
			})
			v.stopLiveSpeechSession(s)
		}
	}()
	return nil
}

func (v *Service) StopLiveSpeech() {
	v.liveMu.Lock()
	s := v.liveSpeech
	v.liveSpeech = nil
	v.liveMu.Unlock()
	if s != nil {
		s.cancel()
		_ = s.conn.CloseNow()
	}
}

func (v *Service) currentLiveSpeech(sessionID string) (*liveSpeechSession, error) {
	v.liveMu.Lock()
	defer v.liveMu.Unlock()
	if v.liveSpeech == nil || v.liveSpeech.id != sessionID {
		return nil, errors.New("live speech session is not active")
	}
	return v.liveSpeech, nil
}

func (v *Service) stopLiveSpeechSession(s *liveSpeechSession) {
	v.liveMu.Lock()
	if v.liveSpeech == s {
		v.liveSpeech = nil
	}
	v.liveMu.Unlock()
	s.cancel()
	_ = s.conn.CloseNow()
}

func (v *Service) emitLiveSpeech(event liveSpeechEventData) {
	if v.emit != nil {
		v.emit(liveSpeechEvent, event)
	}
}

func (v *Service) readLiveSpeech(s *liveSpeechSession) {
	defer close(s.done)
	defer v.stopLiveSpeechSession(s)
	for {
		_, data, err := s.conn.Read(s.ctx)
		if err != nil {
			if s.ctx.Err() == nil && s.finishing.Load() {
				v.emitLiveSpeech(liveSpeechEventData{SessionID: s.id, Kind: "done"})
			} else if s.ctx.Err() == nil {
				message := liveConnectionError(s.provider, err)
				log.Printf("live speech connection error (%s): %s", s.provider, message)
				v.emitLiveSpeech(liveSpeechEventData{SessionID: s.id, Kind: "error", Message: message})
			}
			return
		}
		for _, event := range normalizeLiveSpeechMessage(s.id, s.provider, data) {
			if event.Kind == "error" {
				log.Printf("live speech provider error (%s): %s", s.provider, event.Message)
			}
			v.emitLiveSpeech(event)
		}
	}
}

func liveConnectionError(provider string, err error) string {
	name := map[string]string{EndpointOpenAI: "OpenAI", EndpointGemini: "Gemini API", EndpointVertex: "Vertex AI"}[provider]
	if name == "" {
		name = "Live speech"
	}
	detail := strings.TrimSpace(err.Error())
	if len(detail) > 500 {
		detail = detail[:500] + "…"
	}
	if detail == "" {
		detail = "connection closed"
	}
	return name + ": " + detail
}

func normalizeLiveSpeechMessage(sessionID, provider string, data []byte) []liveSpeechEventData {
	var message map[string]any
	if json.Unmarshal(data, &message) != nil {
		return []liveSpeechEventData{{SessionID: sessionID, Kind: "error", Message: "invalid live speech response"}}
	}
	if remote, ok := message["error"].(map[string]any); ok {
		return []liveSpeechEventData{{SessionID: sessionID, Kind: "error", Message: liveProviderError(provider, remote)}}
	}
	if provider == EndpointOpenAI {
		kind, _ := message["type"].(string)
		item, _ := message["item_id"].(string)
		switch kind {
		case "conversation.item.input_audio_transcription.delta":
			text, _ := message["delta"].(string)
			if text != "" {
				return []liveSpeechEventData{{SessionID: sessionID, Kind: "delta", ItemID: item, Text: text}}
			}
		case "conversation.item.input_audio_transcription.completed":
			text, _ := message["transcript"].(string)
			return []liveSpeechEventData{{SessionID: sessionID, Kind: "final", ItemID: item, Text: text}}
		case "error":
			return []liveSpeechEventData{{SessionID: sessionID, Kind: "error", Message: "OpenAI live transcription failed"}}
		}
		return nil
	}
	content, _ := message["serverContent"].(map[string]any)
	if content == nil {
		content, _ = message["server_content"].(map[string]any)
	}
	if content == nil {
		return nil
	}
	result := []liveSpeechEventData{}
	for _, field := range []struct{ key, alt, kind string }{
		{"interimInputTranscription", "interim_input_transcription", "interim"},
		{"inputTranscription", "input_transcription", "final"},
	} {
		part, _ := content[field.key].(map[string]any)
		if part == nil {
			part, _ = content[field.alt].(map[string]any)
		}
		text, _ := part["text"].(string)
		if text != "" {
			result = append(result, liveSpeechEventData{SessionID: sessionID, Kind: field.kind, Text: text})
		}
	}
	return result
}

func liveProviderError(provider string, remote map[string]any) string {
	name := map[string]string{EndpointOpenAI: "OpenAI", EndpointGemini: "Gemini API", EndpointVertex: "Vertex AI"}[provider]
	if name == "" {
		name = "Live speech"
	}
	message, _ := remote["message"].(string)
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		message = message[:500] + "…"
	}
	details := []string{}
	for _, key := range []string{"code", "type", "status", "param"} {
		if value, ok := remote[key]; ok && value != nil && fmt.Sprint(value) != "" {
			details = append(details, fmt.Sprintf("%s=%v", key, value))
		}
	}
	if message == "" {
		message = "live transcription failed"
	}
	if len(details) > 0 {
		message += " (" + strings.Join(details, ", ") + ")"
	}
	return name + ": " + message
}

// Kept as a seam for protocol-focused tests without a live service.
var wsjsonWrite = func(ctx context.Context, conn *websocket.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}
