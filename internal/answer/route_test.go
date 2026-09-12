package answer

import "testing"

func TestRouteReadsTheQuestion(t *testing.T) {
	cases := []struct {
		question string
		want     Mode
		decided  bool
	}{
		{"", ModeRecap, true},
		{"海野", ModePerson, true},
		{"この人だれだっけ", ModePerson, true},
		{"鍵を拾った場面はどこだっけ", ModeScene, true},
		{"ここまでのあらすじを教えて", ModeRecap, true},
		{"読み終わったので伏線を振り返りたい", ModeReview, true},
		{"who is the man at the station", ModePerson, true},
		{"いま起きていることの意味がよく分からないので説明してほしい", ModeCurrent, false},
	}
	for _, test := range cases {
		mode, decided := Route(test.question)
		if mode != test.want || decided != test.decided {
			t.Errorf("Route(%q) = %v, %v; want %v, %v", test.question, mode, decided, test.want, test.decided)
		}
	}
}

// A question the wording cannot place must still be answerable with no model
// configured: the rule's fallback stands.
func TestClassifyFallsBackWithoutAModel(t *testing.T) {
	if got := classify(t.Context(), Deps{}, "むずかしい質問", ModeCurrent); got != ModeCurrent {
		t.Fatalf("mode = %v", got)
	}
}
