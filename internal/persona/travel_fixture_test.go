package persona

// testTravelSkill is a user-installed travel persona used to exercise routing,
// replies and previews across multiple subjects. It is not a shipped persona.
var testTravelSkill = Skill{
	ID: "move", Name: "michi", Icon: "🚃",
	Domain:       "行き先へ行く予定・希望や移動の話。美容院などの種類だけでもよい。経路・手段・所要時間は担当が調べて提案する。",
	Tone:         "短く要点だけ言う。",
	Memory:       "分野は Place（よく行く場所）の1つ。場所とそこまでの手段を短く残す。",
	Instructions: "行き先と経路を確認して答える。推測は推測と明記する。",
	Tools:        []string{"route", "place_search", "web_search"},
}
