package server

import "time"

// The signed request locale selects speech; session attributes cannot override it.
type alexaVoice string

func (v alexaVoice) language() string {
	switch v {
	case "en-US", "en-GB", "en-CA", "en-AU", "en-IN":
		return "en"
	default:
		return "ja"
	}
}

func (v alexaVoice) text(ja string) string {
	if v.language() == "en" {
		if en, ok := alexaEnglish[ja]; ok {
			return en
		}
	}
	return ja
}

func (v alexaVoice) defaultZone() string {
	// A language/marketplace does not identify the user's timezone.
	if v.language() == "en" {
		return "UTC"
	}
	return "Asia/Tokyo"
}

func (v alexaVoice) dateTime(at time.Time) string {
	if v.language() != "en" {
		return at.Format("1月2日15時04分")
	}
	if v == "en-US" {
		return at.Format("January 2, 2006 at 3:04 PM")
	}
	return at.Format("2 January 2006 at 3:04 PM")
}

var alexaEnglish = map[string]string{
	"どうぞ。": "Go ahead.",
	"かけらノートです。どうしますか。":                     "Memory Timeline. What would you like to do?",
	"もう一度お願いします。":                          "Please try again.",
	"聞き取れませんでした。もう一度お願いします。":               "I didn't catch that. Please try again.",
	"投稿してよければ『はい』、言い直すなら『いいえ』と言ってください。":    "Say yes to post, or no to try again.",
	"メモの内容をどうぞ。":                           "What would you like to save?",
	"返信の内容をどうぞ。":                           "What would you like to reply?",
	"先に『返事を聞いて』で、返信する返事を聞いてください。":          "First say, read my replies, to hear the reply you want to respond to.",
	"『%s』。この内容で投稿しますか。はい、または、いいえで答えてください。": "I heard: %s. Shall I post it? Say yes or no.",
	"確認待ちの投稿はありません。『メモして』か『返信して』と言ってください。": "There is no note waiting for confirmation. Say, take a note, or, reply.",
	"返信先が見つかりません。もう一度『返事を聞いて』と言ってください。":    "That reply is no longer available. Say, read my replies, again.",
	"投稿しました。": "Posted.",
	"『返信して』で、この返事に返信できます。": "Say, reply, to respond to this reply.",
	alexaHelp: "Welcome to Memory Timeline. Say, take a note, followed by what you want to save. You can also say, read my replies, or what's on my schedule.",
	"『メモして』に続けて内容を話すか、『返事を聞いて』『予定を教えて』と言ってください。": "Say, take a note, followed by your message, read my replies, or what's on my schedule.",
	"またいつでも話しかけてください。":                             "Talk to you again soon.",
	"Alexaアプリで、かけらったのアカウントを連携してください。":              "Please link your Kakeratta account in the Alexa app.",
	"今は接続できません。少し待ってからもう一度お試しください。":                "I can't connect right now. Please try again shortly.",
	"Alexaアプリで、かけらったのアカウントを連携し直してください。":            "Please link your Kakeratta account again in the Alexa app.",
	"『メモして』に続けて、かけらの内容を話してください。":                   "Say, take a note, followed by what you want to save.",
	"短いかけらを一つずつ話してください。担当の指名は一人までです。":              "Please give me one short note at a time, mentioning at most one persona.",
	"受け付けを確認できませんでした。アプリで確認してください。":                "I couldn't confirm that your note was saved. Please check the app.",
	"かけらを受け付けました。担当から返事が届いたら、『返事を聞いて』で聞けます。":       "Your note is saved. When a persona replies, say, read my replies, to hear it.",
	"図や画像を含む返事です。アプリで確認してください。":                    "This reply includes a visual. Please view it in the app.",
	"。続きはアプリで確認してください。":                            ". You can read the rest in the app.",
	"返事を読み込めませんでした。少し待ってからお試しください。":                "I couldn't load your replies. Please try again shortly.",
	"最近のかけらには、まだ担当からの返事がありません。少し待ってからもう一度聞いてください。": "There are no persona replies to your recent notes yet. Please ask again shortly.",
	"最近のかけらに届いた返事です。":                              "Here are the replies to your recent notes.",
	"ほかの返事はアプリで確認できます。":                            "You can read more replies in the app.",
	"担当":       "your persona",
	"外部エージェント": "the external agent",
	"内蔵AI":     "the built-in AI",
	"%sから。%s":  "From %s. %s",
	"予定を読み込めませんでした。少し待ってからお試しください。": "I couldn't load your schedule. Please try again shortly.",
	"ほかの予定はアプリで確認できます。":             "You can find more scheduled items in the app.",
	"%s、%s。%s。": "%s, %s. %s.",
	"これからの予定は登録されていません。":  "You have no upcoming scheduled items.",
	"これからの予定です。時刻は%sです。 ": "Here is your upcoming schedule. Times are in %s. ",
}
