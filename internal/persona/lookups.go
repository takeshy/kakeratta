package persona

import "strings"

// lookups is a turn's budget for the tools that look things up: web search,
// places, routes. A live run spent one turn on two hundred web searches, most
// of them the same query, before it said a line. A turn now gets as many
// lookups as it gets reads, and the same call twice is answered from the
// first time rather than made again.
//
// Web search has a smaller budget of its own inside that: each search spends
// the user's Tavily credit, and one or two searches are usually enough to
// know which result to rest on.
type lookups struct {
	limit    int
	used     int
	searches int
	searched int
	seen     map[string]string
}

func newLookups(limit, searches int) *lookups {
	return &lookups{limit: limit, searches: searches, seen: map[string]string{}}
}

const lookupsSpent = "これ以上は調べられません。分かっていることで speak か silent で終えてください。"

const searchesSpent = "このターンの web_search はもう使えません。これまでの検索結果か、ほかの道具で進めてください。"

// take says whether one more call may be made. When the same call was made
// before, its output comes back with ok false and the call is not made again;
// when the budget is spent, a refusal comes back the same way.
func (l *lookups) take(name, args string) (answer string, ok bool) {
	key := name + " " + strings.TrimSpace(args)
	if out, done := l.seen[key]; done {
		return out + "\n**同じ呼び出しは前にも行い、結果は上のとおりです。もう一度は行いません。**", false
	}
	if l.used >= l.limit {
		return lookupsSpent, false
	}
	if name == "web_search" {
		if l.searched >= l.searches {
			return searchesSpent, false
		}
		l.searched++
	}
	l.used++
	return "", true
}

// keep remembers what a call answered, for the next time it is asked.
func (l *lookups) keep(name, args, out string) {
	l.seen[name+" "+strings.TrimSpace(args)] = out
}

func (l *lookups) left() int { return l.limit - l.used }
