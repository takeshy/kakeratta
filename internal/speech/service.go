// Package speech is the dictation stack, carried over whole from speech-popup
// (which in turn is the port of gemihub-desktop's). The engines, the送信 phrase,
// the replacement rules and the live streaming path are the same code, so a
// reader who already dictates in those apps dictates the same way here.
//
// It is a service rather than a set of App methods so it can be tested without
// a WebView: everything it needs from the app - the settings, the event sink,
// the file dialog, where to keep credentials - is handed in.
package speech

import (
	"context"
	"sync"

	"github.com/takeshy/dokking/internal/store"
)

// The endpoint and provider identifiers shared with the frontend.
const (
	EndpointOpenAI  = "openai"
	EndpointWhisper = "whisper-cpp"
	EndpointCustom  = "custom"
	EndpointGemini  = "gemini-transcribe"
	EndpointVertex  = "vertex-transcribe"

	ProviderBrowser = "browser"
	ProviderAPI     = "openai-compatible"
	ProviderLive    = "live"
)

// Service carries the dictation stack's state: the live session, and the Vertex
// credentials.
type Service struct {
	settings func() store.SpeechSettings
	emit     func(name string, data any)
	openFile func(title, pattern string) (string, error)
	openURL  func(target string) error
	root     string
	ctx      context.Context

	liveMu     sync.Mutex
	liveSpeech *liveSpeechSession
}

// New wires the service to the app around it.
func New(ctx context.Context, root string, settings func() store.SpeechSettings,
	emit func(name string, data any), openFile func(title, pattern string) (string, error),
	openURL func(target string) error,
) *Service {
	return &Service{
		ctx: ctx, root: root, settings: settings,
		emit: emit, openFile: openFile, openURL: openURL,
	}
}
