package persona

import (
	"encoding/json"
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
	// The point of the sizes is the contrast, so the ones that ship have to be
	// spread across the tiers rather than piled into one.
	sizes := map[Tier]int{}
	for _, skill := range Builtins {
		sizes[skill.Model]++
	}
	if sizes[TierLow] == 0 {
		t.Fatal("nothing ships in the low tier, so nothing goes first")
	}
	if sizes[TierLow] == len(Builtins) {
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
	active := catalogue.Active([]string{"wine", "gone", "tech"})
	if len(active) != 2 || active[0].ID != "wine" || active[1].ID != "tech" {
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
	skill := Skill{ID: "gourmet", Name: "たべ"}
	for _, mention := range []string{"gourmet", "たべ", "GOURMET"} {
		if !skill.Answers(mention) {
			t.Errorf("%q should reach it", mention)
		}
	}
	for _, mention := range []string{"", "みち", "tech"} {
		if skill.Answers(mention) {
			t.Errorf("%q should not reach it", mention)
		}
	}
}
