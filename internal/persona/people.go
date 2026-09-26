package persona

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// KindPerson marks a persona that keeps one person in mind.
const KindPerson = "person"

// maxTerms bounds a person's names and keywords together. Near words are
// spelled out one by one, so the list is longer than a person would type.
const maxTerms = 80

// IsPerson reports whether this persona keeps a person rather than a subject.
func (s Skill) IsPerson() bool { return s.Kind == KindPerson }

// Subjects are the personas the router decides for. People are left out of
// it: a router asked about 佐藤さん would hand over every post with a 佐藤 in
// it, a politician's included, and a person's persona is only worth having
// when what reaches it is about that person.
func Subjects(skills []Skill) []Skill {
	var out []Skill
	for _, skill := range skills {
		if !skill.IsPerson() {
			out = append(out, skill)
		}
	}
	return out
}

// People are the personas that keep a person.
func People(skills []Skill) []Skill {
	var out []Skill
	for _, skill := range skills {
		if skill.IsPerson() {
			out = append(out, skill)
		}
	}
	return out
}

// Heard returns, for each person whose name or keyword is in the text, the
// word it was found by. The words are compared folded - width, case, kana and
// a trailing honorific do not matter - so 佐藤さん, 佐藤くん and サトウ are the
// same call when all three are listed or the first is.
func Heard(skills []Skill, text string) map[string]string {
	folded := foldText(text)
	if folded == "" {
		return nil
	}
	out := map[string]string{}
	for _, skill := range People(skills) {
		for _, term := range skill.Terms() {
			if key := foldTerm(term); key != "" && strings.Contains(folded, key) {
				out[skill.ID] = term
				break
			}
		}
	}
	return out
}

// Terms are the words a person is heard by: the persona's own name, then its
// aliases, then its keywords.
func (s Skill) Terms() []string {
	out := []string{s.Name}
	out = append(out, s.Aliases...)
	return append(out, s.Keywords...)
}

// honorifics are dropped from the end of a name, so a name listed with one
// still matches the same person called with another.
var honorifics = []string{"さん", "くん", "ちゃん", "さま", "様", "君", "氏", "先生"}

func foldTerm(term string) string {
	folded := foldText(term)
	for _, suffix := range honorifics {
		if trimmed := strings.TrimSuffix(folded, suffix); trimmed != folded && trimmed != "" {
			return trimmed
		}
	}
	return folded
}

// foldText makes width, case and kana not matter, and drops spaces, so "佐藤 さん"
// in a post is the 佐藤さん in the list.
func foldText(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(norm.NFKC.String(text)) {
		switch {
		case unicode.IsSpace(r):
			continue
		case r >= 'ァ' && r <= 'ヶ':
			r -= 'ァ' - 'ぁ'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TidyTerms drops blank, overlong and repeated names and keywords - repeated
// as matched, so さとう after サトウ is gone - and keeps to the
// limit, names first. Only a person keeps any.
func (s *Skill) TidyTerms() {
	if !s.IsPerson() {
		s.Aliases, s.Keywords = nil, nil
		return
	}
	seen := map[string]bool{foldTerm(s.Name): true}
	room := maxTerms
	keep := func(terms []string) []string {
		var out []string
		for _, term := range terms {
			term = strings.TrimSpace(term)
			key := foldTerm(term)
			if key == "" || seen[key] || len([]rune(term)) > 40 || room == 0 {
				continue
			}
			seen[key] = true
			room--
			out = append(out, term)
		}
		return out
	}
	s.Aliases = keep(s.Aliases)
	s.Keywords = keep(s.Keywords)
}

// heardInstructions tell a person woken by a word that a word is all it was.
// 母 is in 母校 and 父母, and 佐藤 in every other 佐藤: the app cannot tell, so
// the persona decides, and the default for a stranger's 佐藤 is to do nothing
// at all - no reply and no note.
func heardInstructions(skill Skill, term string) string {
	return "\n\nこの投稿は「" + term + "」という語が含まれていたため、あなた（" + skill.Name + "）に届きました。" +
		"語が一致しただけで、" + skill.Name + "本人のことや" + skill.Name + "との出来事とは限りません（別の人、同じ字を含む別の言葉など）。" +
		"スレッドと自分のメモから、" + skill.Name + "のことだと判断できる場合だけ対応してください。" +
		"そうでなければ、発言もメモの読み書きもせず、何もしないで終えてください。"
}
