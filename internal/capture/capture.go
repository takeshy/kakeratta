// Package capture is what the app does to a post before anyone sees it.
//
// An image is read once, here, and the text is shared. Handing the picture to
// every persona would mean paying for vision once per persona for the same
// screenshot - and none of the persona models take one anyway. So the read
// says everything a persona might later want to know: the text, what is in
// it, where it seems to come from, and how it looks.
//
// Nothing here judges the content. Whether a post matters, and to whom, is the
// personas' business - the app only turns a picture into something they can all
// read.
package capture

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
)

const readPrompt = `画面のキャプチャや写真を読み取ります。次のJSONだけを返してください。

{"text":"画像内の文字を原文のまま。改行も保つ。文字がなければ空文字",
 "describe":"何が写っているかの説明（2〜4文）。あとから誰かが尋ねそうな見た目の特徴も含める: 物なら色・形・素材・数量・状態、ラベルや包装なら図柄・配色・配置、料理なら盛り付けや量、画面なら何のどの画面か",
 "origin":"出典の手がかり（アプリ名・サイト名・書名・著者・UIの特徴など。分からなければ空文字）"}

規則:
- 文字は要約せず、見えるとおりに写す。縦書きは読み順に直してよい。
- 推測で固有名詞を足さない。読めないものは書かない。
- 見た目の特徴（色・形・数・状態）は、文字が無くても describe に書く。担当はこの説明だけを見て、画像そのものは見ない。
- 画像の内容について意見や感想を書かない。`

// Read looks at an image once and fills in what everyone will share.
//
// A failure is recorded on the media rather than raised: a capture the model
// could not read is still a post, and the personas can still see the line the
// user wrote under it.
func Read(ctx context.Context, client *llm.Client, model string, image []byte, mime string) timeline.Media {
	media := timeline.Media{MIME: mime}
	if client == nil || client.BaseURL == "" || model == "" {
		media.Error = "画像を読むモデルが設定されていません"
		return media
	}
	reply, err := client.Chat(ctx, llm.Request{
		Model: model, JSON: true,
		Files: []llm.Attachment{{Name: "capture" + extension(mime), MIME: mime, Data: image}},
		Messages: []llm.Message{
			{Role: "system", Content: readPrompt},
			{Role: "user", Content: "この画像を読み取ってください。"},
		},
	})
	if err != nil {
		media.Error = err.Error()
		return media
	}
	var parsed struct {
		Text     string `json:"text"`
		Describe string `json:"describe"`
		Origin   string `json:"origin"`
	}
	if err := json.Unmarshal([]byte(firstJSON(reply)), &parsed); err != nil {
		// A model that ignored the shape still looked at the picture.
		media.Describe = strings.TrimSpace(reply)
	} else {
		media.Text = strings.TrimSpace(parsed.Text)
		media.Describe = strings.TrimSpace(parsed.Describe)
		media.Origin = strings.TrimSpace(parsed.Origin)
	}
	media.Model = model
	now := time.Now()
	media.ReadAt = &now
	return media
}

func extension(mime string) string {
	switch {
	case strings.Contains(mime, "png"):
		return ".png"
	case strings.Contains(mime, "webp"):
		return ".webp"
	case strings.Contains(mime, "gif"):
		return ".gif"
	default:
		return ".jpg"
	}
}

// firstJSON pulls the object out of a reply that came wrapped in prose or a
// fenced block.
func firstJSON(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if fence := strings.Index(trimmed, "```"); fence >= 0 {
		rest := trimmed[fence+3:]
		if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
			rest = rest[newline+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			trimmed = strings.TrimSpace(rest[:end])
		}
	}
	start := strings.IndexByte(trimmed, '{')
	end := strings.LastIndexByte(trimmed, '}')
	if start < 0 || end <= start {
		return trimmed
	}
	return trimmed[start : end+1]
}
