package persona

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type routeExperimentTransport func(*http.Request) (*http.Response, error)

func (f routeExperimentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Test-only request rewrite keeps the production prompt, schema and parser unchanged.
func TestRouteExclusionReasons(t *testing.T) {
	if os.Getenv("KAKERATTA_ROUTE_EXCLUSION") != "1" {
		t.Skip("explicit paid experiment opt-in required")
	}
	runRouteReasonExperiment(t, false)
}

func TestRouteConditionMatching(t *testing.T) {
	if os.Getenv("KAKERATTA_ROUTE_CONDITIONS") != "1" {
		t.Skip("explicit paid experiment opt-in required")
	}
	runRouteReasonExperiment(t, true)
}

const conditionMatchPrompt = `投稿内容と各担当条件の一致を判定する分類器です。

全担当の担当条件を1つずつ、今回の対象発言の内容と照合してください。
- 投稿の本文と画像の説明・読み取られた文字を判定対象にします。
- スレッドの文脈で省略された話題や対象を補い、今回の対象発言に含まれる訂正や変更を優先します。
- 条件文の例示は、その条件に含まれる内容の例です。例示だけに限定せず、条件文全体の意味で照合します。
- 条件文に書かれていない前提や制約は足しません。
- 単語が登場するだけではなく、投稿が表している内容が条件に一致するかを判定します。
- 各担当を独立に判定します。一致する担当は0人でも複数でも構いません。
- 時間の表現は「今から」「週末」「仕事帰り」なども文面どおりに解釈します。

deliver ツールで分類結果を返してください。
deliver: 条件に一致する担当のid一覧。
excluded_reasons: 条件に一致しない担当のidごとに、不一致の根拠を短い一文。
表示名ではなくidを使い、全担当をどちらか一方に必ず含めてください。`

func runRouteReasonExperiment(t *testing.T, conditions bool) {
	var snapshot struct {
		Skills  []Skill
		Entries []timeline.Entry
	}
	read := func(path string, value any) {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, value); err != nil {
			t.Fatal(err)
		}
	}
	read("/tmp/kakeratta-production-routing-context.json", &snapshot)
	normalized := conditions && os.Getenv("KAKERATTA_ROUTE_NORMALIZED_CONDITIONS") == "1"
	if normalized {
		criteria := map[string]string{
			"move":           "具体的な地名・駅名・店名・施設名を目的地とする移動、経路、所要時間、出発・到着時刻、旅程、乗り物・チケットの内容がある。行き先の具体名がない用事や、地名が登場するだけの別の話題は含まない。",
			"routine":        "これから行う行動とその時期、繰り返す行動、またはその行動を行ったかどうかの内容がある。時期は時刻・日付に限らず、相対的な時期・生活場面による時期も含む。",
			"book":           "本・読書に関する内容がある。書名・著者・読書の感想・引用・電子書籍の画像・書籍内容への疑問や追加発言を含む。画像の説明や出典に書籍とある場合は、その本の主題を問わず含む。書籍とは無関係な技術ドキュメント単独は含まない。",
			"gourmet":        "食べもの・料理・飲みもの・飲食店・メニュー・食事写真・空腹・食べるものの選択に関する内容がある。単語が登場するだけの食とは無関係な内容は含まない。",
			"purine-checker": "料理・食事・食材の写真が含まれる、または料理・食材のプリン体や痛風の食事管理に関する内容がある。",
			"wine-assistant": "ワインの画像、品種・産地・ヴィンテージ・味わい・料理との組み合わせ、ワインの好みや選択に関する内容がある。",
		}
		for i := range snapshot.Skills {
			condition, ok := criteria[snapshot.Skills[i].ID]
			if !ok {
				t.Fatal("no normalized condition for active persona")
			}
			snapshot.Skills[i].Domain = condition
		}
	}
	if os.Getenv("KAKERATTA_ROUTE_MOVE_PROPOSAL") == "1" {
		for i := range snapshot.Skills {
			if snapshot.Skills[i].ID == "move" {
				for _, builtin := range Builtins {
					if builtin.ID == "move" {
						snapshot.Skills[i].Domain = builtin.Domain
					}
				}
			}
		}
	}
	if os.Getenv("KAKERATTA_ROUTE_COMPACT") == "1" {
		for i := range snapshot.Skills {
			for _, builtin := range Builtins {
				if builtin.ID == snapshot.Skills[i].ID {
					snapshot.Skills[i].Domain = builtin.Domain
				}
			}
			switch snapshot.Skills[i].ID {
			case "purine-checker":
				snapshot.Skills[i].Domain = "料理・食事の写真。または食材のプリン体や痛風の食事管理に関する話。"
			case "wine-assistant":
				snapshot.Skills[i].Domain = "ワインの写真や話。銘柄・品種・産地・味わい・料理との相性・好み・選び方。"
			}
		}
	}
	var baseline []struct {
		ID           string
		Measurements []struct {
			Model   string
			Targets []string
		}
	}
	read("/tmp/kakeratta-production-routing.json", &baseline)
	if selected := os.Getenv("KAKERATTA_ROUTE_CASE"); selected != "" {
		index, err := strconv.Atoi(selected)
		if err != nil || index < 1 || index > len(baseline) {
			t.Fatal("invalid case number")
		}
		baseline = baseline[index-1 : index]
	}
	if repeated := os.Getenv("KAKERATTA_ROUTE_REPEAT"); repeated != "" {
		count, err := strconv.Atoi(repeated)
		if err != nil || count < 1 || count > 5 {
			t.Fatal("repeat must be 1..5")
		}
		original := slices.Clone(baseline)
		for i := 1; i < count; i++ {
			baseline = append(baseline, original...)
		}
	}

	key, err := os.ReadFile(os.Getenv("KAKERATTA_ROUTE_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		ID            string
		Targets       []string
		Excluded      map[string]string
		Error         string
		Input, Output int
		USD, Seconds  float64
	}
	var results []result
	var input, output, agreement, invalid, missingReasons int
	var seconds float64
	for n, previous := range baseline {
		var trigger timeline.Entry
		for _, e := range snapshot.Entries {
			if e.ID == previous.ID {
				trigger = e
				break
			}
		}
		if trigger.ID == "" {
			t.Fatal("trigger not found")
		}
		thread := timeline.Thread(snapshot.Entries, timeline.RootOf(trigger))
		for i, e := range thread {
			if e.ID == trigger.ID {
				thread = thread[:i+1]
				break
			}
		}
		if os.Getenv("KAKERATTA_ROUTE_USER_ONLY") == "1" {
			filtered := make([]timeline.Entry, 0, len(thread))
			for _, entry := range thread {
				if entry.Speaker == timeline.User {
					filtered = append(filtered, entry)
				}
			}
			thread = filtered
		}
		meter := &routeMeter{}
		excluded := map[string]string{}
		api := llm.New(store.NebiusBaseURL, strings.TrimSpace(string(key)))
		api.HTTP.Transport = routeExperimentTransport(func(req *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			req.Body.Close()
			messages := body["messages"].([]any)
			prompt := strings.Split(routePrompt, "\ndeliver を呼んで答えてください。")[0] + `

全担当を1人ずつ確認し、対象発言がその担当領域に当てはまるか判断してください。
選ばない担当については、その担当領域と対象発言のどこが合わないか、具体的な理由を短い一文で書いてください。
理由を確認して領域に当てはまると分かった担当は除外せず、deliver に入れてください。ただし迷っただけで渡してはいけません。
deliver を呼んで答えてください。excluded_reasons には選ばない全担当のidと除外理由を、deliver には選ぶ担当のidを入れてください。
表示名ではなくidを使い、全担当をどちらか一方に必ず含めてください。選んだ理由は不要です。`
			messages[0].(map[string]any)["content"] = prompt
			function := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
			function["parameters"] = map[string]any{
				"type": "object", "properties": map[string]any{
					"excluded_reasons": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "選ばない全担当のidごとの、担当領域と対象発言が合わない具体的理由を短い一文で"},
					"deliver":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "渡す担当のid。誰にも渡さないなら空"},
				}, "required": []string{"excluded_reasons", "deliver"},
			}
			if conditions {
				messages[0].(map[string]any)["content"] = conditionMatchPrompt
				if os.Getenv("KAKERATTA_ROUTE_REVISED") == "1" {
					messages[0].(map[string]any)["content"] = routePrompt
				}
				user := messages[1].(map[string]any)
				user["content"] = strings.Replace(user["content"].(string), "担当:\n", "担当条件（id・表示名・条件文）:\n", 1)
				function["description"] = "投稿内容が一致する担当条件を分類する。"
				properties := function["parameters"].(map[string]any)["properties"].(map[string]any)
				properties["excluded_reasons"].(map[string]any)["description"] = "条件に一致しない担当のidごとの不一致の根拠"
				properties["deliver"].(map[string]any)["description"] = "条件に一致する担当のid一覧。一致なしなら空"
			}
			raw, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(raw))
			req.ContentLength = int64(len(raw))
			res, err := meter.RoundTrip(req)
			if err != nil {
				return res, err
			}
			payload, err := io.ReadAll(res.Body)
			res.Body.Close()
			res.Body = io.NopCloser(bytes.NewReader(payload))
			if err != nil {
				return res, err
			}
			var reply struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
						Calls   []struct {
							Function struct {
								Name      string
								Arguments string
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"message"`
				} `json:"choices"`
			}
			if json.Unmarshal(payload, &reply) == nil && len(reply.Choices) > 0 {
				m := reply.Choices[0].Message
				chosen := firstJSON(m.Content)
				for _, c := range m.Calls {
					if c.Function.Name == "deliver" {
						chosen = c.Function.Arguments
					}
				}
				var decision struct {
					Excluded map[string]string `json:"excluded_reasons"`
				}
				if json.Unmarshal([]byte(chosen), &decision) == nil {
					excluded = decision.Excluded
				}
			}
			return res, nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		start := time.Now()
		targets := Route(ctx, api, store.NemotronLightning, snapshot.Skills, thread, trigger)
		elapsed := time.Since(start).Seconds()
		cancel()
		if (meter.err != nil && meter.err.Error() != "invalid routing response") || meter.input == 0 {
			t.Fatalf("case %d API failure: %v", n+1, meter.err)
		}
		slices.Sort(targets)
		failure := ""
		if meter.err != nil {
			failure = meter.err.Error()
			invalid++
		}
		complete := true
		for _, skill := range snapshot.Skills {
			_, hasReason := excluded[skill.ID]
			if slices.Contains(targets, skill.ID) == hasReason || (hasReason && strings.TrimSpace(excluded[skill.ID]) == "") {
				complete = false
			}
		}
		if !complete {
			missingReasons++
		}
		var ultra []string
		for _, m := range previous.Measurements {
			if m.Model == store.NemotronUltra {
				ultra = m.Targets
			}
		}
		same := failure == "" && slices.Equal(targets, ultra)
		if same {
			agreement++
		}
		row := result{trigger.ID, targets, excluded, failure, meter.input, meter.output,
			(float64(meter.input)*.06 + float64(meter.output)*.24) / 1e6, elapsed}
		results = append(results, row)
		raw, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		resultPath := "/tmp/kakeratta-routing-exclusion.json"
		if conditions {
			resultPath = "/tmp/kakeratta-routing-conditions.json"
		}
		if normalized {
			resultPath = "/tmp/kakeratta-routing-normalized-conditions.json"
		}
		if os.Getenv("KAKERATTA_ROUTE_REVISED") == "1" {
			resultPath = "/tmp/kakeratta-routing-revised.json"
		}
		if os.Getenv("KAKERATTA_ROUTE_CASE") != "" {
			resultPath = "/tmp/kakeratta-routing-case-full.json"
			if os.Getenv("KAKERATTA_ROUTE_USER_ONLY") == "1" {
				resultPath = "/tmp/kakeratta-routing-case-user-only.json"
			}
		}
		if os.Getenv("KAKERATTA_ROUTE_MOVE_PROPOSAL") == "1" {
			resultPath = "/tmp/kakeratta-routing-move-proposal.json"
		}
		if os.Getenv("KAKERATTA_ROUTE_COMPACT") == "1" {
			resultPath = "/tmp/kakeratta-routing-compact.json"
		}
		if err := os.WriteFile(resultPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
		input += meter.input
		output += meter.output
		seconds += elapsed
		t.Logf("CASE %d agreeUltra=%t excludedComplete=%t error=%q", n+1, same, complete, failure)
	}
	cost := (float64(input)*.06 + float64(output)*.24) / 1e6
	t.Log(fmt.Sprintf("SUMMARY cases=%d agreement=%d invalid=%d incomplete_exclusions=%d input=%d output=%d USD=%.6f JPY/route@150=%.4f mean_seconds=%.2f",
		len(results), agreement, invalid, missingReasons, input, output, cost, cost*150/float64(len(results)), seconds/float64(len(results))))
}
