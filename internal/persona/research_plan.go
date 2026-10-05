package persona

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/takeshy/kakeratta/internal/llm"
)

const researchPlanningInstruction = `
# 初回の調査方針
この最初のresearchでは、会話・返信先の親回答を読み、ユーザーは何を求め、そのために何を確認し、どの深さで答えるべきかをまとめて判断してください。
回答に必要な確認事項をresearch_planとして整理し、その要求と観点に照らしてdeepを決めてください。readまたはfinish_readにresearch_planとdeepを含め、readなら最初のstepsも同時に提出してください。別の判定callは不要です。
判断例:
- 親: エージェントループの短い概要 / 返信: 詳しく → deep=true。親回答の手順や役割を詳しく説明するための確認項目をplanにする。
- 複数の技術方式について、障害時の動作と整合性保証・制約を比較する依頼 → deep=true。既知の知識だけでも、複数の観点を統合した正確な回答が必要。
- 親: 午後から雨 / 返信: 何時ごろ？ → deep=false。時間帯予報の確認だけで十分。
- お礼だけ → deep=false、research_plan=[]、finish_read。
- deepは難易度だけでなく、ユーザーの要求と十分な回答に必要な品質から判断します。
- 親回答に対する「詳しく」「もっと詳しく」「詳しく教えて」「深掘りして」「具体的に説明して」「ちゃんと調べて」「もう一度考えて」「それって本当？」などは、文脈上その回答の詳細化・再検討・検証を求めていればdeep=trueです。特に短い「詳しく」も親回答をより詳しく説明する要求として扱ってください。単純なキーワード一致にはしません。
- 明示語がなくても、複数情報の比較・統合、前回答の再検討、慎重な正誤検証、複数回の調査、強い推論が必要ならdeep=trueにしてください。追加readの要否と回答の深さは別です。既知の情報だけで答えられ、finish_readを選ぶ場合も、複数の設計・保証・制約を比較して統合する依頼にはdeep=trueが必要です。
- 「ありがとう」「なるほど」「了解」「そうなんだ」「助かった」などの会話継続だけならdeep=falseです。簡単な追加条件や「午後から雨」への「何時ごろ？」も、時間帯予報だけで十分ならdeep=falseです。
- research_planはこの回答のために確認する短い調査項目だけです。最大5項目、各120文字以内の短い一文。思考過程やreasoning、長い説明を書かず、単純な質問は1項目、比較や検証では確認する観点を短く挙げてください。お礼など確認する事項がなければ空で構いません。ユーザーに表示する必要はありません。
- 後続roundではdeepやresearch_planを再提出せず、計画を参考にreadまたはfinish_readを判断してください。
`

type researchDecision struct {
	Deep bool
	Plan []string
}

// Copy the schema: finishReadTool is shared, and later rounds must retain
// their ordinary read/finish_read interface.
func withResearchPlanning(tool llm.Tool) llm.Tool {
	tool.Description = "初回research: 依頼の要求と必要な回答品質を分析し、短いresearch_planとdeepを確定する。" + tool.Description
	if tool.Name == finishReadTool.Name {
		tool.Description += "追加readが不要でも、詳しい説明の要求や慎重な比較・統合が必要ならdeep=true。調査不要だからdeep=falseとはしない。"
	}
	params := make(map[string]any, len(tool.Parameters))
	for key, value := range tool.Parameters {
		params[key] = value
	}
	properties := map[string]any{}
	if original, ok := params["properties"].(map[string]any); ok {
		for key, value := range original {
			properties[key] = value
		}
	}
	properties["deep"] = map[string]any{"type": "boolean", "description": "依頼の要求と必要な回答品質から、通常より深い回答が必要か。慎重な比較・統合・検証ならtrue。read不要でも浅い回答とは限らない。"}
	properties["research_plan"] = map[string]any{"type": "array", "maxItems": 5, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 120}, "description": "確認すべき短い調査項目のみ。思考過程は含めない。"}
	required := []any{"deep", "research_plan"}
	switch original := params["required"].(type) {
	case []string:
		for _, key := range original {
			required = append(required, key)
		}
	case []any:
		required = append(required, original...)
	}
	params["properties"], params["required"] = properties, required
	tool.Parameters = params
	return tool
}

func parseResearchDecision(args string, reading bool) (researchDecision, error) {
	var request struct {
		Deep  *bool           `json:"deep"`
		Plan  *[]string       `json:"research_plan"`
		Steps json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal([]byte(args), &request); err != nil {
		return researchDecision{}, fmt.Errorf("初回researchの形式が不正です: %w", err)
	}
	if request.Deep == nil || request.Plan == nil || len(*request.Plan) > 5 {
		return researchDecision{}, fmt.Errorf("初回researchにはdeepとresearch_plan（最大5項目）が必要です。")
	}
	// Validate the read payload before committing the decision or expanding
	// budgets. Malformed steps must not spend a read or choose an answer model.
	if reading {
		var steps []ReadStep
		if err := json.Unmarshal(request.Steps, &steps); err != nil {
			return researchDecision{}, fmt.Errorf("readのstepsが不正です: %w", err)
		}
		if len(steps) == 0 {
			return researchDecision{}, fmt.Errorf("初回readには空でないstepsが必要です。調査不要ならfinish_readしてください。")
		}
	}
	for i, item := range *request.Plan {
		item = strings.TrimSpace(item)
		if item == "" || utf8.RuneCountInString(item) > 120 {
			return researchDecision{}, fmt.Errorf("research_planは各120文字以内の短い調査項目にしてください。")
		}
		(*request.Plan)[i] = item
	}
	return researchDecision{Deep: *request.Deep, Plan: *request.Plan}, nil
}
