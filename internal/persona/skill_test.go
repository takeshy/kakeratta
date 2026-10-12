package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
)

func TestSupportCanReadSpecificationsAndAnswerDirectMentions(t *testing.T) {
	skill, ok := NewCatalogue(nil).Find(context.Background(), "support")
	if !ok || !skill.Builtin || skill.IsPerson() || !skill.Answers("support") {
		t.Fatalf("support persona = %+v, found = %t", skill, ok)
	}
	set := tools.New(tools.Config{}).For(skill.Tools).As(skill.ID)
	if !set.IsReadOnly("kakeratta_help") || set.Allows("schedule") || set.Allows("web_search") {
		t.Fatalf("support capabilities = %v", set.Declarations())
	}
	result, err := set.Run(context.Background(), "kakeratta_help", tools.Args{}, nil)
	if err != nil || !strings.Contains(result.Text, `okf_version: "0.2"`) {
		t.Fatalf("support cannot read its specification: %+v, %v", result, err)
	}
}

func TestMichiIsRetiredAndUserTravelDefinitionsRemainAvailable(t *testing.T) {
	ctx := context.Background()
	catalogue := NewCatalogue(skillStore(t, t.TempDir()))
	if _, ok := catalogue.Find(ctx, "move"); ok {
		t.Fatal("michi still ships as a built-in persona")
	}
	if active := catalogue.Active(ctx, []string{"move", "support"}); len(active) != 1 || active[0].ID != "support" {
		t.Fatalf("saved retired activation = %+v", active)
	}
	raw, err := json.Marshal(testTravelSkill)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalogue.Install(raw, nil); err != nil {
		t.Fatal(err)
	}
	travel, ok := catalogue.Find(ctx, "move")
	if !ok || travel.Builtin || !travel.Answers("michi") {
		t.Fatalf("user-installed travel definition = %+v, found = %t", travel, ok)
	}
	if err := catalogue.Uninstall("move"); err != nil {
		t.Fatal(err)
	}
}

func TestTheBuiltInDomainsAreFarApart(t *testing.T) {
	// Choosing who to ask only means something when the answer is not obvious.
	// If two shipped domains read the same, @ has nothing to pick between.
	for i, one := range Builtins {
		for _, other := range Builtins[i+1:] {
			if one.Domain == other.Domain {
				t.Fatalf("%s and %s have the same domain", one.ID, other.ID)
			}
		}
		if strings.TrimSpace(one.Domain) == "" {
			t.Fatalf("%s has no domain, so nothing can be routed to it", one.ID)
		}
		if err := validate(one); err != nil {
			t.Fatalf("%s: %v", one.ID, err)
		}
	}
}

// The domain is public - the app reads it to route. The instructions are not:
// how to keep notes and when to speak are the skill's own business.
func TestTheDeclarationSaysWhatItShould(t *testing.T) {
	raw, err := json.Marshal(Builtins[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"domain", "instructions", "tools"} {
		if _, found := fields[name]; !found {
			t.Fatalf("a skill must declare %q", name)
		}
	}
}

func TestInstallRefusesWhatItCannotTrust(t *testing.T) {
	catalogue := NewCatalogue(skillStore(t, t.TempDir()))
	known := func(tool string) bool { return tool == "web_search" }
	cases := map[string]string{
		"パス区切りを含むID": `{"id":"../evil","name":"x","domain":"y","model":"low"}`,
		"担当領域が無い":    `{"id":"ok","name":"x","domain":"","model":"low"}`,
		"内蔵と同じID":    `{"id":"gourmet","name":"x","domain":"y","model":"low"}`,
		"知らない道具":     `{"id":"ok","name":"x","domain":"y","model":"low","tools":["nuke"]}`,
		// The names the external agent answers to are reserved.
		"予約されたID":          `{"id":"agent","name":"x","domain":"y","model":"low"}`,
		"予約された名前 agent":    `{"id":"ok","name":"Agent","domain":"y","model":"low"}`,
		"予約された名前 エージェント":   `{"id":"ok","name":"エージェント","domain":"y","model":"low"}`,
		"予約された名前 外部エージェント": `{"id":"ok","name":"外部エージェント","domain":"y","model":"low"}`,
		"予約された呼び名":         `{"id":"ok","name":"x","kind":"person","aliases":["ｅｒｒ","エージェントさん"],"domain":"y","model":"low"}`,
	}
	for name, definition := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := catalogue.Install([]byte(definition), known); err == nil {
				t.Fatal("installed something it should have refused")
			}
		})
	}
}

func TestInstalledSkillsJoinTheCatalogue(t *testing.T) {
	catalogue := NewCatalogue(skillStore(t, t.TempDir()))
	known := func(tool string) bool { return tool == "web_search" }
	skill, err := catalogue.Install([]byte(
		`{"id":"wine","name":"ぶどう","icon":"🍷","domain":"ワイン","instructions":"銘柄を残す","model":"high","tools":["web_search"]}`), known)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Builtin {
		t.Fatal("an installed skill is not a built-in one")
	}
	found, ok := catalogue.Find(context.Background(), "wine")
	if !ok || found.Name != "ぶどう" {
		t.Fatalf("not in the catalogue: %+v", found)
	}
	// Order is the order the user put them in, and ids that no longer resolve
	// are dropped rather than breaking the room.
	active := catalogue.Active(context.Background(), []string{"wine", "gone", "routine"})
	if len(active) != 2 || active[0].ID != "wine" || active[1].ID != "routine" {
		t.Fatalf("active = %+v", active)
	}
	if err := catalogue.Uninstall("wine"); err != nil {
		t.Fatal(err)
	}
	if _, ok := catalogue.Find(context.Background(), "wine"); ok {
		t.Fatal("uninstalled skill still offered")
	}
	if err := catalogue.Uninstall("gourmet"); err == nil {
		t.Fatal("a built-in skill was removed")
	}
}

func TestAnsweringToAName(t *testing.T) {
	skill := Skill{ID: "gourmet", Name: "tabe"}
	for _, mention := range []string{"gourmet", "tabe", "Tabe", "GOURMET"} {
		if !skill.Answers(mention) {
			t.Errorf("%q should reach it", mention)
		}
	}
	for _, mention := range []string{"", "michi", "routine"} {
		if skill.Answers(mention) {
			t.Errorf("%q should not reach it", mention)
		}
	}
}

func TestUpdateBuiltinPreservesIdentityAcrossReload(t *testing.T) {
	root := t.TempDir()
	c := NewCatalogue(skillStore(t, root))
	skill := Builtins[0]
	skill.Domain = "修正した担当領域"
	raw, _ := json.Marshal(skill)
	if _, err := c.Update(context.Background(), skill.ID, raw, nil); err != nil {
		t.Fatal(err)
	}
	reloaded := NewCatalogue(skillStore(t, root))
	got, ok := reloaded.Find(context.Background(), skill.ID)
	if !ok || got.Domain != skill.Domain || !got.Builtin || !got.Customized {
		t.Fatalf("updated builtin: %+v", got)
	}
	if len(reloaded.All(context.Background())) != len(Builtins) {
		t.Fatal("duplicate builtin")
	}
	skill.ID = "changed"
	raw, _ = json.Marshal(skill)
	if _, err := c.Update(context.Background(), Builtins[0].ID, raw, nil); err == nil {
		t.Fatal("changed identity")
	}
}

// sona prepares for upcoming outings as well as following up on small chores.
func TestSonaShipsWithPreparationAndWeather(t *testing.T) {
	var sona *Skill
	for i := range Builtins {
		if Builtins[i].ID == "tech" || Builtins[i].Name == "kuda" {
			t.Fatal("kuda still ships")
		}
		if Builtins[i].ID == "routine" {
			sona = &Builtins[i]
		}
	}
	if sona == nil || sona.Name != "sona" {
		t.Fatal("sona does not ship")
	}
	if len(sona.Tools) != 2 || sona.Tools[0] != "schedule" || sona.Tools[1] != "weather_forecast" {
		t.Fatalf("sona's tools = %v", sona.Tools)
	}
	if sona.Answers("plana") || !sona.Answers("sona") || !sona.Answers("routine") {
		t.Fatal("sona must replace plana while retaining its storage ID")
	}
	// The domain is what the routing reads, so both kinds have to be in it.
	// A time on a sentence is what makes it sona's, whatever the sentence is
	// about: "20時30分からドラマを見る" went to nobody while the domain read as a
	// list of chores.
	for _, kind := range []string{"繰り返し", "一度きり", "いつ何をするか", "実施したか", "今から", "持ち物", "忘れ物", "天気", "外出前"} {
		if !strings.Contains(sona.Domain, kind) {
			t.Fatalf("sona's domain lacks %q", kind)
		}
	}
	for _, rule := range []string{"schedule", "時刻は聞かない", "催促しない", "飲み忘れと判断しない", "やる前に急かさない"} {
		if !strings.Contains(sona.Instructions, rule) {
			t.Fatalf("sona's instructions lack %q", rule)
		}
	}
	for _, rule := range []string{
		"本体から渡されたその日のかけらを確認する",
		"sona 自身が同じ用について今日すでに声をかけた記録",
		"声をかける根拠がある場合だけ一度だけ",
	} {
		if !strings.Contains(sona.Instructions, rule) {
			t.Fatalf("sona's schedule wake instructions lack %q", rule)
		}
	}
	// A schedule written into a note as prose registers nothing: the app reads
	// the store, never the note. These three came from watching sona write
	// "予定: 17:00" and a raw entry id into its notes and call no tool at all.
	for _, rule := range []string{
		"schedule 道具で登録したものだけが有効",
		"id が無い予定の行は書かない",
		// A schedule nobody can see is indistinguishable from none: sona says so
		// once, in reply to the post that occasioned it.
		"登録できたら、その投稿への返事として一言だけ知らせる",
	} {
		if !strings.Contains(sona.Instructions, rule) {
			t.Fatalf("sona's instructions lack %q", rule)
		}
	}
}

func TestScheduleWakeRequiresTimelineCheckBeforeSpeaking(t *testing.T) {
	for _, rule := range []string{
		"本体が今日の00:00から現在までの直近のかけらを下に渡しています",
		"済ませた・行った・飲んだ",
		"あなた自身が同じ用について今日すでに声をかけている場合",
		"声をかける根拠がある場合だけ",
	} {
		if !strings.Contains(scheduleInstructions, rule) {
			t.Fatalf("schedule wake prompt lacks %q", rule)
		}
	}
	if strings.Contains(scheduleInstructions, "必ず timeline_search") {
		t.Fatal("schedule wake still makes a tool call a prerequisite for speaking")
	}
}

func TestTabeAnswersRestaurantSearchRequests(t *testing.T) {
	var tabe *Skill
	for i := range Builtins {
		if Builtins[i].ID == "gourmet" {
			tabe = &Builtins[i]
		}
	}
	if tabe == nil {
		t.Fatal("tabe does not ship")
	}
	for _, want := range []string{"店や食事の候補を探す相談には必ず答える", "web_search", "place_search", "実在する候補"} {
		if !strings.Contains(tabe.Instructions, want) {
			t.Fatalf("tabe's instructions lack %q", want)
		}
	}
}

// skillStore is a workspace at root, opened afresh each time as a restart
// would open it.
func skillStore(t *testing.T, root string) *store.Store {
	t.Helper()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A persona saved under a reserved name before the reservation stays readable.
func TestReservedNamesAreCheckedOnlyWhenWritten(t *testing.T) {
	if _, err := parseInstalled("agent", []byte(`{"id":"agent","name":"agent","domain":"y","model":"low"}`)); err != nil {
		t.Fatalf("an existing definition was refused: %v", err)
	}
	if err := Validate(Skill{ID: "ok", Name: "AGENT", Domain: "y"}, nil); err == nil || !strings.Contains(err.Error(), "予約") {
		t.Fatalf("a draft took a reserved name: %v", err)
	}
}
