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
	people := People(skills)
	names := callNameMatches(people, folded)
	for _, skill := range people {
		if terms := heardIn(skill, folded, names, true); len(terms) > 0 {
			out[skill.ID] = terms[0]
		}
	}
	return out
}

// HeardTerms are all of a person's words found in the text, in the order of
// Terms. A post that should not have reached a person can be in it by more
// than one word, and each is one the person may want to remove.
func HeardTerms(skill Skill, text string, others ...Skill) []string {
	folded := foldText(text)
	if folded == "" {
		return nil
	}
	names := callNameMatches(append([]Skill{skill}, People(others)...), folded)
	return heardIn(skill, folded, names, false)
}

type callNameMatch struct {
	key        string
	start, end int
}

// Names compete at each occurrence, so パパ inside 加奈ちゃんのパパ does
// not name the user's father. A separate パパ elsewhere still can. Equal
// names remain ambiguous; shared topic keywords never compete here.
func callNameMatches(skills []Skill, folded string) []callNameMatch {
	var out []callNameMatch
	seen := map[string]bool{}
	for _, skill := range skills {
		for _, name := range append([]string{skill.Name}, skill.Aliases...) {
			key := foldTerm(name)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			for offset := 0; offset < len(folded); {
				at := strings.Index(folded[offset:], key)
				if at < 0 {
					break
				}
				start := offset + at
				out = append(out, callNameMatch{key: key, start: start, end: start + len(key)})
				offset = start + 1
			}
		}
	}
	return out
}

func nameHeard(key string, names []callNameMatch) bool {
	for _, match := range names {
		if match.key != key {
			continue
		}
		covered := false
		for _, other := range names {
			if len(other.key) > len(key) && other.start <= match.start && other.end >= match.end {
				covered = true
				break
			}
		}
		if !covered {
			return true
		}
	}
	return false
}

func heardIn(skill Skill, folded string, names []callNameMatch, first bool) []string {
	var out []string
	for i, term := range skill.Terms() {
		key := foldTerm(term)
		matched := key != "" && strings.Contains(folded, key)
		if i <= len(skill.Aliases) {
			matched = nameHeard(key, names)
		}
		if matched {
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
	personMemory = "その人について分かったこと（好み・誕生日・記念日・家族・仕事など）と、その人との出来事（会話・贈り物・約束・貸し借り・会う予定。済んでいないことを先に）を覚える。タグには話題と場所を付ける。"
	personRules  = `その人本人のことと、その人との出来事を覚えます。
- 貸し借りや約束など済んでいないことは、済んだと分かるまで忘れません。
- ユーザーが返事をして、到着・感想・結果を教えたことや確認不要と伝えたことは、その確認を終了します。贈った出来事は残しても、返答済みの確認を未完了の用事として残さず、記憶の該当する事実を update または invalidate で整えます。単なる相づちから到着や完了を推測しません。
- あなた自身の「あとで様子を聞く」という発言は、ユーザーの約束や確認依頼ではありません。未完了の用事として記憶せず、自分の発言を根拠に確認や schedule を繰り返しません。
- 定期起床で贈り物などの経過を確認する前に、記憶と timeline_search でその話題についての過去の声かけとユーザーの返事を確かめます。前回のチェックイン以前の記録も対象です。返答済みなら黙ります。一度確認した話題は、返事がなくても、新しいユーザーの情報や明示的な再確認依頼がなければ再び尋ねません。記録を取得できず確認状況が分からない場合も黙ります。
- 会う予定が分かったら、schedule でその日の朝に自分を起こす予定を置きます。起きたら、前回の出来事や済んでいないことを一言添えて知らせます。
- 誕生日・記念日と、日時の分かる会う予定・約束・予約の通知は、基本情報の保存時とユーザーのかけらを受け取ったときに本体が登録します。誕生日・記念日は毎年、1週間前と当日の朝に通知します。同じ対象の予定が一覧にあれば追加して重複させないでください。会話で分かった事実や出来事は記憶に残りますが、基本情報を記憶に複製する必要はありません。通知時は、そのことと相手の好みや前回の出来事を短く添えてください。`
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
		"- ユーザーが" + name + "にしたこと（贈った・連絡した・会った）を話したときは、" + name + "のふりをせず、ユーザーに向けて、その出来事を受け止める短い返事をします。毎回記憶したと宣言したり、頼まれていない経過確認を約束したりしません。" +
		name + "がどう受け取ったか、何と言うかは、ユーザーが話したこと以外は書きません。\n" +
		"- 出来事の向き（誰が誰に何をしたか）を取り違えません。" + name + "がユーザーにしたこと（もらった・いただいた・助けてもらった・誘われた）を話したときは、受け取ったのはユーザーです。" +
		"あなたが受け取った側としてお礼を言ったり、" + name + "が喜んでいると書いたりせず、ユーザーが" + name + "から受け取った出来事として、ユーザーに向けて短く受け止めます。\n\n"
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
		s.Instructions += "\n\n" + s.Name + "の基本情報（ユーザーが担当の設定として保存した内容）：\n" + s.BasicInfo + "\n基本情報は記憶に複製せず、回答・調査・予定の判断で参照します。基本情報の修正はユーザーが担当の編集画面で行います。記憶に古い情報がある場合は、この基本情報を優先します。誕生日・記念日の予定も基本情報を参照してください。"
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
		"スレッドと自分の記憶から、" + skill.Name + "のことだと判断できる場合だけ対応してください。" +
		"そうでなければ、発言せず、何もしないで終えてください。"
}
