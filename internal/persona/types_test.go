package persona

import (
	"reflect"
	"testing"
)

// The fields a persona's memory layout names are the kinds it keeps a summary
// of; a persona that names none keeps one.
func TestTheLayoutNamesTheFields(t *testing.T) {
	layout := "分野は Wine（銘柄。産地・品種・感想）と Preference（好みの傾向）の2つ。OKF の形式で書く。"
	if got := allowedTypes(layout); !reflect.DeepEqual(got, []string{"Wine", "Preference"}) {
		t.Fatalf("fields = %v", got)
	}
	if got := memoryFields(Skill{}); !reflect.DeepEqual(got, []string{defaultField}) {
		t.Fatalf("a persona without a layout keeps %v", got)
	}
	want := map[string][]string{
		"infographic": nil,
		"support":     nil,
		"gourmet":     {"Preference", "Shop"},
		"book":        {"Book", "Theme"},
		"routine":     {"Routine", "Errand"},
	}
	for _, builtin := range Builtins {
		if got := memoryFields(builtin); !reflect.DeepEqual(got, want[builtin.ID]) {
			t.Fatalf("%s keeps fields %v, want %v", builtin.ID, got, want[builtin.ID])
		}
	}
	person := Skill{ID: "mom", Name: "母", Kind: KindPerson}
	if got := memoryFields(person); !reflect.DeepEqual(got, []string{"Fact", "Event"}) {
		t.Fatalf("a person keeps fields %v", got)
	}
}
