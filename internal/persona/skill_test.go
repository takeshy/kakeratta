package persona

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

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
	// The point of the sizes is the contrast, so the ones that ship are spread
	// across more than one tier. None of them is low: they all hold tools that
	// do more than look, and those need a workflow.
	sizes := map[Tier]int{}
	for _, skill := range Builtins {
		sizes[skill.Model]++
		if skill.Model == TierLow && len(lowToolsOnly(skill.Tools)) != len(skill.Tools) {
			t.Fatalf("%s ships in the low tier with tools the low tier cannot hold", skill.ID)
		}
	}
	if len(sizes) < 2 {
		t.Fatal("everything ships in one tier, so there is no contrast")
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
	for _, name := range []string{"domain", "instructions", "model", "tools"} {
		if _, found := fields[name]; !found {
			t.Fatalf("a skill must declare %q", name)
		}
	}
}

func TestInstallRefusesWhatItCannotTrust(t *testing.T) {
	catalogue := NewCatalogue(t.TempDir())
	known := func(tool string) bool { return tool == "web_search" }
	cases := map[string]string{
		"パス区切りを含むID":  `{"id":"../evil","name":"x","domain":"y","model":"low"}`,
		"担当領域が無い":     `{"id":"ok","name":"x","domain":"","model":"low"}`,
		"知らないモデルの大きさ": `{"id":"ok","name":"x","domain":"y","model":"gigantic"}`,
		"内蔵と同じID":     `{"id":"gourmet","name":"x","domain":"y","model":"low"}`,
		"知らない道具":      `{"id":"ok","name":"x","domain":"y","model":"low","tools":["nuke"]}`,
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
	catalogue := NewCatalogue(t.TempDir())
	known := func(tool string) bool { return tool == "web_search" }
	skill, err := catalogue.Install([]byte(
		`{"id":"wine","name":"ぶどう","icon":"🍷","domain":"ワイン","instructions":"銘柄を残す","model":"high","tools":["web_search"]}`), known)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Builtin {
		t.Fatal("an installed skill is not a built-in one")
	}
	found, ok := catalogue.Find("wine")
	if !ok || found.Name != "ぶどう" {
		t.Fatalf("not in the catalogue: %+v", found)
	}
	// Order is the order the user put them in, and ids that no longer resolve
	// are dropped rather than breaking the room.
	active := catalogue.Active([]string{"wine", "gone", "routine"})
	if len(active) != 2 || active[0].ID != "wine" || active[1].ID != "routine" {
		t.Fatalf("active = %+v", active)
	}
	if err := catalogue.Uninstall("wine"); err != nil {
		t.Fatal(err)
	}
	if _, ok := catalogue.Find("wine"); ok {
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
	c := NewCatalogue(root)
	skill := Builtins[0]
	skill.Domain = "修正した担当領域"
	raw, _ := json.Marshal(skill)
	if _, err := c.Update(skill.ID, raw, nil); err != nil {
		t.Fatal(err)
	}
	reloaded := NewCatalogue(root)
	got, ok := reloaded.Find(skill.ID)
	if !ok || got.Domain != skill.Domain || !got.Builtin || !got.Customized {
		t.Fatalf("updated builtin: %+v", got)
	}
	if len(reloaded.All()) != len(Builtins) {
		t.Fatal("duplicate builtin")
	}
	skill.ID = "changed"
	raw, _ = json.Marshal(skill)
	if _, err := c.Update(Builtins[0].ID, raw, nil); err == nil {
		t.Fatal("changed identity")
	}
}

// plana keeps small repeated chores by the part of the day. It works by the
// clock, calls nothing outside the app, and treats medicine lightly.
func TestPlanaShipsInPlaceOfKuda(t *testing.T) {
	var plana *Skill
	for i := range Builtins {
		if Builtins[i].ID == "tech" || Builtins[i].Name == "kuda" {
			t.Fatal("kuda still ships")
		}
		if Builtins[i].ID == "routine" {
			plana = &Builtins[i]
		}
	}
	if plana == nil || plana.Name != "plana" {
		t.Fatal("plana does not ship")
	}
	// schedule is the app's own; nothing plana declares leaves the deployment.
	if len(plana.Tools) != 1 || plana.Tools[0] != "schedule" {
		t.Fatalf("plana's tools = %v", plana.Tools)
	}
	// Reading an intention out of a passing remark, working out when to look in,
	// and calling a tool in the same turn is not the light tier's work.
	if plana.Model != TierMiddle {
		t.Fatalf("plana = %+v", *plana)
	}
	// The domain is what the routing reads, so both kinds have to be in it.
	// A time on a sentence is what makes it plana's, whatever the sentence is
	// about: "20時30分からドラマを見る" went to nobody while the domain read as a
	// list of chores.
	for _, kind := range []string{"繰り返し", "一度きり", "いつ何をするか", "実施したか", "今から"} {
		if !strings.Contains(plana.Domain, kind) {
			t.Fatalf("plana's domain lacks %q", kind)
		}
	}
	for _, rule := range []string{"schedule", "時刻は聞かない", "催促しない", "飲み忘れと判断しない", "やる前に急かさない"} {
		if !strings.Contains(plana.Instructions, rule) {
			t.Fatalf("plana's instructions lack %q", rule)
		}
	}
	// A schedule written into a note as prose registers nothing: the app reads
	// the store, never the note. These three came from watching plana write
	// "予定: 17:00" and a raw entry id into its notes and call no tool at all.
	for _, rule := range []string{
		"schedule 道具で登録したものだけが有効",
		"id が無い予定の行は書かない",
		"本文に投稿のIDやリンクを書かない",
		"指示されていない項目を足さない",
		// A schedule nobody can see is indistinguishable from none: plana says so
		// once, in reply to the post that occasioned it.
		"登録できたら、その投稿への返事として一言だけ知らせる",
	} {
		if !strings.Contains(plana.Instructions, rule) {
			t.Fatalf("plana's instructions lack %q", rule)
		}
	}
}

func TestMichiCanResolveDestinationsBeforeRouting(t *testing.T) {
	var michi *Skill
	for i := range Builtins {
		if Builtins[i].ID == "move" {
			michi = &Builtins[i]
		}
	}
	if michi == nil {
		t.Fatal("michi does not ship")
	}
	for _, tool := range []string{"web_search", "place_search", "route"} {
		if !slices.Contains(michi.Tools, tool) {
			t.Fatalf("michi lacks %s: %v", tool, michi.Tools)
		}
	}
	for _, rule := range []string{"正式名称・住所・地域", "座標がないことだけを理由にユーザーへ聞き返さない"} {
		if !strings.Contains(michi.Instructions, rule) {
			t.Fatalf("michi's instructions lack %q", rule)
		}
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

// michi answers with the means that makes sense for the distance: walking when
// it is close, the car when walking would be absurd, both only in between.
func TestMichiPicksAPlausibleMeans(t *testing.T) {
	for _, skill := range Builtins {
		if skill.ID != "move" {
			continue
		}
		for _, rule := range []string{"徒歩30分以内なら徒歩だけ", "徒歩1時間以上なら", "車だけ", "30分〜1時間のときだけ"} {
			if !strings.Contains(skill.Instructions, rule) {
				t.Fatalf("michi lost %q", rule)
			}
		}
		return
	}
	t.Fatal("michi is not built in")
}
