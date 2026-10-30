package persona

import (
	"context"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// This definition is transient: direct questions do not install a persona,
// accumulate private notes, or acquire background schedules.
func builtinAgent() Skill {
	return Skill{
		ID: timeline.Agent, Name: timeline.BuiltinAIName("ja"), Icon: "🤖", MemoryEnabled: memoryDisabled(),
		Domain: "ユーザーから直接呼びかけられた質問。分野を問わず、過去の投稿や位置情報を横断して調べる。",
		Tone:   "質問に直接、根拠を添えて分かりやすく答える。",
		Tools:  []string{"web_search", "schedules_list", "list_personas", "list_notes", "read_note", "get_thread", "kakeratta_help"},
		Instructions: "あなたはkakerattaの内蔵AI。外部エージェントではない。" +
			"kakerattaの使い方・仕様・設定・担当・記憶・通知・検索・連携の質問や困りごとはサポート窓口として答える。" +
			"その場合だけ回答前にkakeratta_helpでindex.mdと質問に関連する章を読み、同梱の仕様を根拠にする。全章を毎回読まず、他の質問では仕様を読まない。" +
			"仕様の英語をそのまま貼らず、ユーザーの言語で画面の項目名と具体的な操作手順を分かりやすく説明する。内部実装は質問に必要な場合だけ説明する。" +
			"仕様で確認した動作と、本人の環境の実際の状態や推測を区別する。仕様を読んだだけでAPIキー・接続・端末・稼働状況を確認したことにはならない。" +
			"仕様にないことは分からないと伝え、不具合の原因を断定しない。必要ならエラーや操作を短く尋ね、秘密のキーやパスワードは求めない。設定変更や修復を実行したと偽らない。" +
			"本人の過去についての質問はtimeline_searchで投稿・位置情報・日時を確認し、根拠に使った投稿を出典にする。" +
			"必要なら期間や語句を変えて再検索する。記録がなければ確認できなかったと伝え、推測で埋めない。" +
			"位置情報は投稿時点の手掛かりであり、移動の正確な時刻や境界通過を断定しない。" +
			"list_personasで担当を確認し、必要に応じてlist_notes/read_noteでその記憶、schedules_listで予定を参照する。" +
			"他の担当の記憶は参考資料であり、本人の過去の事実は可能なら元投稿でも確認する。最新の外部情報は利用可能な場合にweb_searchで確認する。" +
			"私的な担当メモは持たず、他の担当のメモも変更しない。メール送信や外部サービスの操作、予定登録は実行できないので、完了したと答えない。",
	}
}

func (r *Runner) deliverBuiltinAgent(outer context.Context, entry timeline.Entry, place *timeline.Place, client *llm.Client, settings store.Settings) error {
	entries, err := store.SearchThread(outer, r.store, entry.ID)
	if err != nil {
		return err
	}
	thread := timeline.Thread(entries, timeline.RootOf(entry))
	if len(thread) == 0 {
		return nil
	}
	if place != nil {
		thread[0].Place = place
	}
	ctx, cancel := context.WithTimeout(outer, 10*time.Minute)
	defer cancel()
	// The destination was selected during preparation; a later settings
	// change must not detach this turn to an external agent.
	settings.ExternalAgent.Paused = true
	// Direct built-in questions use Super VL for the combined read/answer
	// loop. Explicit depth wording still selects the configured Ultra slot.
	settings.MiddleModel = store.NemotronSuperVL
	skill := builtinAgent()
	skill.Name = timeline.BuiltinAIName(entry.Language)
	names := map[string]string{}
	for _, persona := range r.skills(ctx).All() {
		names[persona.ID] = persona.Name
	}
	registry := r.registry(settings).WithAgentReads(r.store, names)
	if r.run(ctx, skill, thread, entry, true, false, "", false, client, settings, registry) == nil && ctx.Err() == nil {
		r.speak(ctx, skill, timeline.RootOf(entry), couldNotAnswer(entry.Language), nil, r.store.MemoryMeta(skill.ID), entry.ID, nil, "super", false)
	}
	return ctx.Err()
}
