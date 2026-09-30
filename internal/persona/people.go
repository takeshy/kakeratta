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
		if terms := heardIn(skill, folded, true); len(terms) > 0 {
			out[skill.ID] = terms[0]
		}
	}
	return out
}

// HeardTerms are all of a person's words found in the text, in the order of
// Terms. A post that should not have reached a person can be in it by more
// than one word, and each is one the person may want to remove.
func HeardTerms(skill Skill, text string) []string {
	folded := foldText(text)
	if folded == "" {
		return nil
	}
	return heardIn(skill, folded, false)
}

func heardIn(skill Skill, folded string, first bool) []string {
	var out []string
	for _, term := range skill.Terms() {
		if key := foldTerm(term); key != "" && strings.Contains(folded, key) {
			out = append(out, term)
			if first {
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
		s.Aliases, s.Keywords, s.BasicInfo = nil, nil, ""
		return
	}
	s.BasicInfo = strings.TrimSpace(s.BasicInfo)
	// A person's tone and kinds of note are the app's; see forTurn.
	s.Tone, s.Memory = "", ""
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

// How a person's persona speaks, files and behaves is the same for everyone
// it keeps, so the app holds it rather than each persona: a better rule
// reaches every person at once. What a person's persona has of its own is the
// relationship (its domain) and, if any, instructions for this one person.
const (
	personTone   = "ユーザーに向けて、落ち着いて短く。思い出したことを一言添える。"
	personMemory = "型は Fact（その人について分かったこと。好み・誕生日・記念日・家族・仕事など）と Event（その人との出来事。会話・贈り物・約束・貸し借り・会う予定）の2つ。tags には話題と場所を付ける。"
	personRules  = `その人本人のことと、その人との出来事を覚えます。
- 貸し借りや約束など済んでいないことは、済んだと分かるまで忘れません。
- 会う予定が分かったら、schedule でその日の朝に自分を起こす予定を置き、Event のメモに予定の行を残します。起きたら、前回の出来事や済んでいないことを一言添えて知らせます。
- 誕生日・記念日と、日時の分かる会う予定・約束・予約の通知は、基本情報の保存時とユーザーのかけらを受け取ったときに本体が登録します。誕生日・記念日は毎年、1週間前と当日の朝に通知します。同じ対象の予定が一覧にあれば追加して重複させないでください。会話で分かった事実や出来事はメモに残しますが、基本情報をメモに複製する必要はありません。通知時は、そのことと相手の好みや前回の出来事を短く添えてください。`
)

// intro is how a persona is introduced to the model, at the head of every
// prompt it is given. A subject is called by its name. A person's persona is
// not the person: it is the one who looks after the user's dealings with them,
// and introducing it by the person's name made it answer as them - "ありがとう、
// 届いたよ" to a gift the user said they had sent.
func (s Skill) intro() string {
	if s.IsPerson() {
		return "あなたは、ユーザーと「" + s.Name + "」との間を見守る担当です。" + s.Name + "本人ではありません。"
	}
	return "あなたは「" + s.Name + "」。"
}

// personIdentityRules say what a person's persona is, and is not, before what
// it keeps.
func personIdentityRules(name string) string {
	return "あなたは" + name + "本人ではなく、ユーザーと" + name + "との間を見守る担当（コーディネーター）です。\n" +
		"- " + name + "になりきって話しません。" + name + "の返事・気持ち・言葉を、" + name + "に代わって書きません。話す相手は常にユーザーです。\n" +
		"- ユーザーが" + name + "にしたこと（贈った・連絡した・会った）を話したときは、" + name + "のふりをせず、ユーザーに向けて、覚えたことと、あとで確認できること（届いた頃に様子を聞く、など）を一言で返します。" +
		name + "がどう受け取ったか、何と言うかは、ユーザーが話したこと以外は書きません。\n\n"
}

// forTurn is the persona as a turn sees it. A subject is as saved; a person
// gets the app's tone, kinds of note and rules, with its relationship and its
// own instructions set among them.
func (s Skill) forTurn() Skill {
	if !s.IsPerson() {
		return s
	}
	s.Tone = personTone
	s.Memory = personMemory
	s.Domain = s.Name + "本人のこと、または" + s.Name + "との出来事。\nユーザーとの関係性：" + strings.TrimSpace(s.Domain)
	own := strings.TrimSpace(s.Instructions)
	s.Instructions = personIdentityRules(s.Name) + personRules
	if s.BasicInfo != "" {
		s.Instructions += "\n\n" + s.Name + "の基本情報（ユーザーが担当の設定として保存した内容）：\n" + s.BasicInfo + "\n基本情報はメモに複製せず、回答・調査・予定の判断で参照します。基本情報の修正はユーザーが担当の編集画面で行います。メモに古い情報がある場合は、この基本情報を優先します。誕生日・記念日の予定も基本情報を参照してください。"
	}
	if own != "" {
		s.Instructions += "\n\n気を配ること（ユーザーが" + s.Name + "について、あなたに書いた注意）：\n" + own
	}
	return s
}

// heardInstructions tell a person woken by a word that a word is all it was.
// 母 is in 母校 and 父母, and 佐藤 in every other 佐藤: the app cannot tell, so
// the persona decides, and the default for a stranger's 佐藤 is to do nothing
// at all - no reply and no note.
func heardInstructions(skill Skill, term string) string {
	return "\n\nこの投稿は「" + term + "」という語が含まれていたため、" + skill.Name + "を見守るあなたに届きました。" +
		"語が一致しただけで、" + skill.Name + "本人のことや" + skill.Name + "との出来事とは限りません（別の人、同じ字を含む別の言葉など）。" +
		"スレッドと自分のメモから、" + skill.Name + "のことだと判断できる場合だけ対応してください。" +
		"そうでなければ、発言もメモの読み書きもせず、何もしないで終えてください。"
}
