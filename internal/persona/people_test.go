package persona

import (
	"context"
	"encoding/json"
	"github.com/takeshy/kakeratta/internal/tools"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func sato() Skill {
	return Skill{ID: "sato", Name: "佐藤さん", Domain: "佐藤さん本人のこと、または佐藤さんとの出来事。", Kind: KindPerson,
		Aliases: []string{"サトウ", "けんじ"}, Keywords: []string{"釣り", "釣行", "ミナミ食品"}}
}

func TestHeardMatchesNamesAndKeywordsLoosely(t *testing.T) {
	skills := []Skill{Builtins[0], sato()}
	for text, want := range map[string]string{
		"佐藤くんから北海道のお土産もらった": "佐藤さん",
		"さとうさんと飲んだ":         "サトウ",
		"ｹﾝｼﾞが来週来る":         "けんじ",
		"週末は海で釣りをするらしい":     "釣り",
		"みなみ食品の工場見学の話を聞いた":  "ミナミ食品",
	} {
		heard := Heard(skills, text)
		if heard["sato"] != want {
			t.Errorf("%q: heard %q, want %q", text, heard["sato"], want)
		}
		if _, ok := heard["gourmet"]; ok {
			t.Errorf("%q: a subject was matched by words", text)
		}
	}
	if heard := Heard(skills, "石破さんのニュースを見た"); len(heard) != 0 {
		t.Fatalf("heard %v in a post not about the person", heard)
	}
}

func TestHeardTermsListsEveryWordInThePost(t *testing.T) {
	got := HeardTerms(sato(), "さとうさんと釣行、釣りの話")
	if !slices.Equal(got, []string{"サトウ", "釣り", "釣行"}) {
		t.Fatalf("heard %v", got)
	}
	if got := HeardTerms(sato(), "石破さんのニュース"); len(got) != 0 {
		t.Fatalf("heard %v", got)
	}
}

func TestHeardKeepsQualifiedCallNamesAndSharedTopics(t *testing.T) {
	skills := []Skill{
		{ID: "father", Name: "自分の父", Kind: KindPerson, Aliases: []string{"パパ"}, Keywords: []string{"釣り"}},
		{ID: "friend-father", Name: "子供の友人のパパ", Kind: KindPerson, Aliases: []string{"子供の友人のお父さん"}, Keywords: []string{"釣り"}},
		{ID: "daughter-father", Name: "義理の娘のパパ", Kind: KindPerson, Aliases: []string{"義理の娘のお父さん"}, Keywords: []string{"ピアノ"}},
		{ID: "teacher", Name: "子供の先生", Kind: KindPerson},
	}
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"パパと話した", []string{"father"}},
		{"子供の友人のパパと話した", []string{"friend-father"}},
		{"義理の娘のパパと話した", []string{"daughter-father"}},
		{"義理の娘のパパと話した後、パパに電話した", []string{"daughter-father", "father"}},
		{"パパに電話した後、義理の娘のパパと話した", []string{"daughter-father", "father"}},
		{"義理の娘のお父さんと話した", []string{"daughter-father"}},
		{"友人のお父さんと話した", nil},
		{"先生と話した", nil},
		{"子供の先生と話した", []string{"teacher"}},
		{"釣りに行く", []string{"father", "friend-father"}},
		{"義理の娘のパパと釣りに行く", []string{"daughter-father", "father", "friend-father"}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got := Heard(skills, tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("heard %v, want %v", got, tc.want)
			}
			for _, id := range tc.want {
				if got[id] == "" {
					t.Errorf("did not hear %s: %v", id, got)
				}
			}
		})
	}
}

func TestQualifiedCallNameUsedByRoutingChecksAndMonthlyHistory(t *testing.T) {
	runner, workspace, _ := setup(t, func(system, user string) string {
		t.Error("person routing unexpectedly called the model")
		return `{}`
	}, "father", "kana-father")
	father := Skill{ID: "father", Name: "自分の父", Domain: "父", Kind: KindPerson, Aliases: []string{"パパ"}}
	kanaFather := Skill{ID: "kana-father", Name: "加奈ちゃんのパパ", Domain: "子供の友人の父", Kind: KindPerson}
	for _, skill := range []Skill{father, kanaFather} {
		raw, _ := json.Marshal(skill)
		if _, err := runner.catalogue.Install(raw, nil); err != nil {
			t.Fatal(err)
		}
	}
	entry := post(t, workspace, "加奈ちゃんのパパと話した")
	for _, tc := range []struct {
		skill Skill
		want  bool
	}{{father, false}, {kanaFather, true}} {
		check, err := runner.CheckRouting(context.Background(), entry.ID, tc.skill.ID)
		if err != nil || !check.Answered || check.Delivered != tc.want || (len(check.Words) > 0) != tc.want {
			t.Fatalf("%s routing check: %+v, %v", tc.skill.ID, check, err)
		}
		history := runner.monthlyCheckIn(context.Background(), tc.skill, entry.At.Add(-time.Hour), entry.At.Add(time.Hour), time.UTC)
		if strings.Contains(history, entry.Text) != tc.want {
			t.Fatalf("%s monthly history: %s", tc.skill.ID, history)
		}
	}
}

func TestPersonAnswersItsAliases(t *testing.T) {
	person := sato()
	for _, mention := range []string{"佐藤", "サトウ", "さとう", "けんじ", "sato"} {
		if !person.Answers(mention) {
			t.Errorf("@%s did not reach the person", mention)
		}
	}
	if person.Answers("釣り") {
		t.Error("a keyword answered an @")
	}
	subject := Builtins[0]
	subject.Aliases = []string{"food"}
	if subject.Answers("food") {
		t.Error("a subject answered an alias")
	}
}

func TestTidyTermsAndValidate(t *testing.T) {
	person := sato()
	person.Aliases = []string{" 佐藤 ", "さとう", "サトウ", "", "けんじ"}
	person.TidyTerms()
	if !slices.Equal(person.Aliases, []string{"さとう", "けんじ"}) {
		t.Fatalf("aliases = %v", person.Aliases)
	}
	if err := validate(person); err != nil {
		t.Fatal(err)
	}

	subject := Builtins[0]
	subject.Keywords = []string{"ラーメン"}
	if validate(subject) == nil {
		t.Error("a subject was allowed keywords")
	}
	subject.TidyTerms()
	if subject.Keywords != nil || validate(subject) != nil {
		t.Error("tidying did not drop a subject's keywords")
	}

	unknown := sato()
	unknown.Kind = "pet"
	if validate(unknown) == nil {
		t.Error("an unknown kind was allowed")
	}
}

func TestRouteSubjectsDecidesPeopleWithoutTheRouter(t *testing.T) {
	skills := []Skill{sato(), {ID: "mother", Name: "母", Domain: "母のこと。", Kind: KindPerson}}
	trigger := timeline.Entry{ID: "e1", Kind: timeline.KindPost, Text: "佐藤さんとキャンプの話をした"}
	routing := routeSubjects(context.Background(), nil, store.Settings{}, skills, []timeline.Entry{trigger}, trigger)
	if !routing.Answered {
		t.Fatalf("people alone left the routing unanswered: %+v", routing)
	}
	if !slices.Equal(routing.Deliver, []string{"sato"}) || routing.Heard["sato"] != "佐藤さん" {
		t.Fatalf("routing = %+v", routing)
	}
	if routing.Excluded["mother"] == "" {
		t.Fatalf("the person not heard has no reason: %+v", routing)
	}

	// The subjects' router failing does not lose a person heard in the post.
	withSubject := append([]Skill{Builtins[0]}, skills...)
	routing = routeSubjects(context.Background(), nil, store.Settings{}, withSubject, []timeline.Entry{trigger}, trigger)
	if routing.Answered || !slices.Contains(routing.Deliver, "sato") {
		t.Fatalf("routing = %+v", routing)
	}
}

func TestUnmatchedPersonReasonFollowsPostLanguage(t *testing.T) {
	for _, tc := range []struct{ language, want string }{
		{"en", "No name or keyword for this person matched this post."},
		{"ja", "この人物に一致する呼び名・キーワードがありませんでした"},
		{"", "この人物に一致する呼び名・キーワードがありませんでした"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			trigger := timeline.Entry{ID: "shoes", Text: "New running shoes today.", Language: tc.language}
			routing := routeSubjects(context.Background(), nil, store.Settings{}, []Skill{sato()}, []timeline.Entry{trigger}, trigger)
			if len(routing.Deliver) != 0 || routing.Excluded["sato"] != tc.want {
				t.Fatalf("routing = %+v", routing)
			}
		})
	}
}

func TestCheckedKeywordReachesPersonAndIsRecorded(t *testing.T) {
	routes := 0
	runner, workspace, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			routes++
			return `{"deliver":[],"excluded_reasons":{}}`
		}
		return `{"summary":"test","steps":[{"type":"speak","text":"釣りですか"}]}`
	}, "gourmet", "sato")
	person := sato()
	person.Keywords = []string{"キャンプ"}
	install := func() {
		raw, _ := json.Marshal(person)
		if _, err := runner.catalogue.Update(context.Background(), person.ID, raw, nil); err != nil {
			if _, err := runner.catalogue.Install(raw, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	install()
	post := post(t, workspace, "週末は海釣りに行くらしい")

	check, err := runner.CheckRouting(context.Background(), post.ID, "sato")
	if err != nil || !check.Answered || check.Delivered || check.Reason == "" {
		t.Fatalf("check = %+v, %v", check, err)
	}
	person.Keywords = append(person.Keywords, "海釣り")
	install()
	check, err = runner.CheckRouting(context.Background(), post.ID, "sato")
	if err != nil || !check.Delivered || check.Heard != "海釣り" {
		t.Fatalf("check = %+v, %v", check, err)
	}
	if !slices.Equal(check.Words, []string{"海釣り"}) {
		t.Fatalf("words = %v", check.Words)
	}
	if routes != 0 {
		t.Fatalf("checking a person asked the router %d times", routes)
	}
	if err := runner.ApplyRouting(context.Background(), check.Token, post.ID); err != nil {
		t.Fatal(err)
	}
	record, _ := workspace.Routing(post.ID)
	if record == nil || !slices.Contains(record.Delivered, "sato") || record.Heard["sato"] != "海釣り" {
		t.Fatalf("routing = %+v", record)
	}
}

func TestPersonWokenByAWordIsToldItMayNotBeAboutThem(t *testing.T) {
	var prompts []string
	runner, workspace, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":[],"excluded_reasons":{}}`
		}
		prompts = append(prompts, system+"\n"+user)
		return `{"summary":"関係ない","steps":[]}`
	}, "mother")
	raw, _ := json.Marshal(Skill{ID: "mother", Name: "母", Domain: "母のこと。", Kind: KindPerson})
	if _, err := runner.catalogue.Install(raw, nil); err != nil {
		t.Fatal(err)
	}
	entry := post(t, workspace, "母校の同窓会に行った")
	runner.reactEntry(context.Background(), entry.ID, nil, false)
	if len(prompts) == 0 {
		t.Fatal("the person was not woken")
	}
	if !strings.Contains(strings.Join(prompts, "\n"), "「母」という語が含まれていたため") {
		t.Fatal("the person was not told it was reached by a word")
	}

	// Called by name, it is being asked, and is not told to doubt it.
	prompts = nil
	entry = post(t, workspace, "@母 来週の予定どうする?")
	runner.reactEntry(context.Background(), entry.ID, nil, false)
	if strings.Contains(strings.Join(prompts, "\n"), "という語が含まれていたため") {
		t.Fatal("an @ was treated as a word match")
	}
}

// A person's persona is told the app's rules for every person, its
// relationship, and its own instructions; a subject is as saved.
func TestPersonTurnGetsTheAppsRules(t *testing.T) {
	person := sato()
	person.Domain = "大学時代の友人。"
	person.Instructions = "仕事の話は結論から。"
	got := person.forTurn()
	if !strings.Contains(got.Domain, "佐藤さん本人のこと") || !strings.Contains(got.Domain, "ユーザーとの関係性：大学時代の友人。") {
		t.Fatalf("domain = %q", got.Domain)
	}
	if got.Tone != personTone || got.Memory != personMemory {
		t.Fatalf("tone = %q, memory = %q", got.Tone, got.Memory)
	}
	if !strings.HasPrefix(got.Instructions, personIdentityRules("佐藤さん")+personRules) || !strings.HasSuffix(got.Instructions, "気を配ること（ユーザーが佐藤さんについて、あなたに書いた注意）：\n仕事の話は結論から。") {
		t.Fatalf("instructions = %q", got.Instructions)
	}
	if !strings.Contains(got.Instructions, "誕生日") {
		t.Fatal("a person is not told about birthdays")
	}
	person.Instructions = ""
	if got := person.forTurn(); got.Instructions != personIdentityRules("佐藤さん")+personRules {
		t.Fatalf("instructions without its own = %q", got.Instructions)
	}
	if got := Builtins[0].forTurn(); !reflect.DeepEqual(got, Builtins[0]) {
		t.Fatalf("subject changed: %+v", got)
	}
}

func TestPersonKeepsNoToneOrKindsOfNote(t *testing.T) {
	person := sato()
	person.Tone, person.Memory = "丁寧に", "型は Wine"
	person.TidyTerms()
	if person.Tone != "" || person.Memory != "" {
		t.Fatalf("tone = %q, memory = %q", person.Tone, person.Memory)
	}
}

func TestPersonBasicInformationIsAvailableWithoutNotes(t *testing.T) {
	person := sato()
	person.BasicInfo = "誕生日は11月10日。タイ料理が好き。"
	got := person.forTurn()
	if !strings.Contains(got.Instructions, person.BasicInfo) || !strings.Contains(got.Instructions, "基本情報は記憶に複製せず") {
		t.Fatalf("instructions = %q", got.Instructions)
	}
	if got.Memory != personMemory || person.Instructions != "" {
		t.Fatal("basic information changed the note schema or stored instructions")
	}
	person.BasicInfo = "辛くないタイ料理が好き。"
	if got := person.forTurn(); !strings.Contains(got.Instructions, person.BasicInfo) || strings.Contains(got.Instructions, "誕生日は11月10日。") {
		t.Fatalf("old information survived edit: %q", got.Instructions)
	}
}

// A person's persona is not the person. It was introduced by the person's
// name - "あなたは「父」" - and answered a gift the user said they had sent with
// "ありがとう、届いたよ", as the father. Every prompt it is given says who it is.
func TestAPersonsPersonaIsNotIntroducedAsThePerson(t *testing.T) {
	father := Skill{ID: "father", Name: "父", Domain: "父。", Kind: KindPerson}.forTurn()
	subject := Builtins[0].forTurn()

	if got := father.intro(); !strings.Contains(got, "「父」との間を見守る担当") || !strings.Contains(got, "父本人ではありません") || strings.HasPrefix(got, "あなたは「父」。") {
		t.Fatalf("a person's intro = %q", got)
	}
	if got := subject.intro(); got != "あなたは「"+subject.Name+"」。" {
		t.Fatalf("a subject's intro = %q", got)
	}
	for name, prompt := range map[string]string{
		"answer": answerSystem(father, briefRule),
		"phase":  phasePrompt("schedule", father, tools.New(tools.Config{}).For(nil), ""),
		"keeper": keeperPrompt(father, true, nil),
	} {
		if strings.Contains(prompt, "あなたは「父」。") {
			t.Errorf("the %s prompt introduces the persona as 父", name)
		}
		if !strings.Contains(prompt, "父本人ではありません") {
			t.Errorf("the %s prompt does not say the persona is not 父", name)
		}
		// The same follow-up lifecycle must reach answering, schedule planning,
		// and memory keeping, including already installed person definitions.
		for _, want := range []string{"その確認を終了します", "update または invalidate", "ユーザーの約束や確認依頼ではありません", "前回のチェックイン以前", "一度確認した話題は", "確認状況が分からない場合も黙ります"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("the %s prompt lacks follow-up rule %q", name, want)
			}
		}
		if strings.Contains(prompt, "届いた頃に様子を聞く") {
			t.Errorf("the %s prompt still encourages unsolicited delivery checks", name)
		}
	}
	// What it is told to do when the user reports what they did for the person.
	rules := father.Instructions
	// And when the person did something for the user: "佳奈ちゃんのパパから
	// タラの芽をいただいた" was answered "ありがとうございます。佳奈ちゃんのパパも
	// 喜んでいるでしょう", thanking as the recipient and giving the joy to the giver.
	for _, want := range []string{"父になりきって話しません", "話す相手は常にユーザーです", "贈った・連絡した・会った", "出来事の向き", "受け取ったのはユーザーです", "父が喜んでいると書いたりせず"} {
		if !strings.Contains(rules, want) {
			t.Errorf("the rules do not say %q", want)
		}
	}
	if strings.Contains(answerSystem(subject, briefRule), "本人ではありません") {
		t.Error("a subject was told it is not a person")
	}
}
