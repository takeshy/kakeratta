# kakeratta直接依頼ワーカー実装仕様

状態: **実装済み**（`direct_request_worker.py`・`check_kakeratta_gate.py`・integration bundle）  
作成日: 2026-10-14  
対象: OpenShell 0.0.116 / Hermes Agent / Nebius Token Factory Provider v2

## 目的

kakerattaの直接依頼を、親モデルの判断に依存せずコードで取得・生成・Review・実行・返却する。直接依頼ではNemotron Superを起動しない。通常のpersona askは既存のSuper経路を変更しない。

## 現状と問題

現在はWake Gateが直接依頼を検知するとHermes cronの親Superを起動する。Skill v25は親へ `role: "orchestrator"` のUltraを呼ぶよう指示するが、実地試験では以下が発生した。

- 親Superが取得後、委譲前後にファイル・コード・terminal処理を行った。
- `delegate_task` に `role: "orchestrator"` は設定されたが、委譲goalからGenerate–Review手順が脱落した。
- orchestrator Ultraはleafを呼ばず、自分で成果物を生成した。
- 親Superは31回推論して成果物を再処理した。

自然言語のSkillとrole指定だけでは役割分離を強制できない。

## 2026-10-13 Token Factory費用分析

2026-10-13の検証では、Token Factoryの日次表示が約10 USDまで増加した。これはUltraを選択したこと単独ではなく、コードで役割分離する前のHermes cronが、1件の依頼中に親Superと子モデルを反復して呼び出し、増え続ける会話・tool結果を毎回再送したことが主因である。

Token Factoryの日次境界に合わせた2026-10-13 00:00–24:00 UTCのHermes `session_model_usage` 集計は次のとおりだった。`input_tokens`はキャッシュ読取りを含まないため、`cache_read_tokens`を別列にした。`reasoning_tokens`も別の内訳として記録される。

| モデル | API呼出し | 通常入力 | キャッシュ読取り | 出力 | Reasoning |
| --- | ---: | ---: | ---: | ---: | ---: |
| Nemotron 3 Super | 410 | 11,570,430 | 552,704 | 259,191 | 137,997 |
| Nemotron 3 Ultra | 172 | 1,111,321 | 2,968,064 | 202,158 | 19,507 |
| Hermes 4 405B | 12 | 45,441 | 177,376 | 1,735 | 0 |
| **合計** | **594** | **12,727,192** | **3,698,144** | **463,084** | **157,504** |

当時のToken Factory単価を各内訳へ適用した概算は約9 USDであり、Provider表示の「10 USD近く」と整合する。Hermes DBの `estimated_cost_usd` と `actual_cost_usd` はこのProviderについて0、`cost_status=unknown`だったため、USD値はDBの費用列ではなくモデル別token集計とProvider単価から復元した概算である。税、丸め、Provider側の集計規則により請求表示とは差が生じ得る。

日本時間の「昨日」（2026-10-13 00:00–24:00 JST）では462呼出しだった。一方、Providerの日次表示と同じUTC範囲では594呼出しになった。日次費用を照合するときは、必ずProviderのタイムゾーンを確認する。

費用の大きかった単一Superセッションは34回・31回・28回のAPI呼出しを行い、それぞれ通常入力約1.43M・1.35M・1.27M tokensを消費した。Reasoningの追加分は全体の一部にすぎず、主要因は多数回の推論と、各回で拡大した入力contextの再送だった。

この結果から、直接依頼では次を実装上の費用境界とする。

- Superを起動せず、取得、claim、生成、Review、実行、replyをコードの状態機械で進める。
- 生成1回、Review最大3回、修正最大2回という有限回数をコードで強制し、LLM自身にloop継続を判断させない。
- 各LLM呼出しについてモデル、段階、入出力、cache、reasoning token、依頼IDを監査履歴へ記録する。
- 1依頼の呼出し数またはtoken量が設定上限を超えたらfail-closedし、未実行のまま理由を返す。
- idle tickではWake Gateだけを実行し、LLMを呼ばない。

## 適用範囲

- `list_asks.requests` の `status: "pending"` にだけ適用する。
- `list_asks.asks` のpersona askは従来どおりWake Gateが `wakeAgent: true` を返し、Superが `claim_ask` → `submit_answer` を行う。
- pendingの直接依頼を専用ワーカーが処理したtickでは、直接依頼を理由にSuperを起動しない。persona askも同時にpendingなら、直接依頼処理後にSuperをpersona ask用として起動してよい。

## 必須フロー

```text
Wake Gate
  → list_asks
  → direct-request worker
      → claim_request（コード）
      → Ultra生成（読取・Web検索のみ）
      → 別UltraでReview
      → NGなら修正Ultra → 再Review（修正は最大2回）
      → 成果物依頼: 合格成果物をreply_request
      → 操作依頼: 合格Pythonを1回だけ実行 → 結果をreply_request
  → persona askがpendingならwakeAgent:true、それ以外はfalse
```

Superは直接依頼の取得、プロンプト作成、Review、実行、返却のいずれにも関与しない。

## 生成フェーズ

- モデル: `nvidia/Nemotron-3-Ultra-550b-a55b`
- Provider: Nebius Token Factory Provider v2
- reasoning: `high`
- 許可能力: kakerattaのread-only MCP、ローカルread-onlyファイル操作、Web検索・Web取得（実装: Hermesの `file` toolsetは書込みを含むため使わず、ワーカー自身を読み取り専用のstdio MCP `files`（`read_file`・`list_directory`）として登録する）
- 禁止能力: ファイル変更、terminal、コード実行、メール・投稿・設定変更などの副作用
- 元のtrigger、thread、DirectContext、ユーザー制約を省略せず渡す。
- HTML・文章・コードなどの成果物依頼は完全な候補を返す。
- 操作依頼は実行せず、実行可能なPythonプログラムと、実行後にユーザーへ返す結果の形式を返す。

read-only制限はプロンプトではなくツール公開範囲・コードで強制する。

## Reviewフェーズ

生成とは別のUltraリクエストを使う。Reviewerは候補を作ったセッションを共有しない。

Review項目:

- 依頼と全制約を満たすか
- 事実・名称・大文字小文字・文字数・形式が正しいか
- HTMLなら指定viewport、overflow、重なり、可読性を検査したか
- Pythonなら要求された操作だけを行い、余計な副作用がないか
- 秘密情報の読出し・送信、任意パス破壊、権限昇格、永続プロセス、無制限通信がないか
- 実行が冪等でない場合、再試行で副作用を重複させない設計か

Reviewerは機械可読なJSONを返す。

```json
{
  "accepted": false,
  "problems": ["具体的な問題"],
  "candidate_sha256": "..."
}
```

`accepted: true` かつハッシュ一致の場合だけ合格とする。不正JSON、空のproblemsを伴う不合格、ハッシュ不一致は不合格とする。

## 修正ループ

- 初回生成後、修正を最大2回許可する。
- 修正は毎回、新しいUltraリクエストへ元の依頼、完全な現候補、Reviewerの具体的指摘を渡す。
- 各修正版を別のReviewリクエストで再検査する。
- 最大2回の修正後も不合格なら実行せず、残った問題を `reply_request` で報告する。
- 無制限ループは禁止する。

## 操作依頼の実行

- PythonはReview合格後だけ実行する。
- 合格時の `candidate_sha256` と実行直前のバイト列のSHA-256が一致しなければ実行しない。
- 実行は1回だけ。プロセス再試行で同じ副作用を繰り返さないよう、依頼IDとコードハッシュの実行済み記録を確認する。
- timeout、stdout/stderr上限、作業ディレクトリ、環境変数allowlistをコードで設定する。
- APIキー、Provider token、MCP tokenなど不要な秘密を子プロセスへ渡さない。
- OpenShellのネットワークポリシーを維持し、許可されていない宛先へ接続させない。
- 終了コード、stdout、stderr、所要時間を取得し、ユーザー向けに結果を返す。
- Review済みコードを実行できない場合は、実行したと偽らず失敗理由を返す。

## 返却

- ワーカーがMCPの `reply_request(requestId, claimId, text)` を直接呼ぶ。
- 成果物依頼は合格した完全な成果物を加工せず返す。
- 操作依頼は、行った操作、成功・失敗、主要な出力を返す。承認済みPython全文は依頼で要求された場合だけ含める。
- 空文字、20,000文字超、claim ID不一致を送らない。
- 成功後は同じ依頼へ再送しない。

## 監査履歴

依頼ID単位のJSON LinesをHermes home配下の専用ディレクトリへ追記する。1イベント1行とし、プロセスクラッシュ後にも経過を復元できるよう各段階でflushする。

記録項目:

- request ID、claim ID、event、UTC時刻
- モデル、Provider、reasoning設定
- generation/review/correctionの試行番号
- 候補のSHA-256と文字数（候補全文は原則記録しない）
- Reviewの合否と問題一覧
- Python実行の開始・終了、コードハッシュ、終了コード、所要時間
- stdout/stderrの切詰め後の記録
- `reply_request` の成功・失敗とentry ID
- Hermes実行ごとの段階、セッションID、API呼出し数、通常入力・キャッシュ読取り・キャッシュ書込み・出力・reasoningトークン（`model_usage`）と、依頼単位の累計

記録禁止:

- APIキー、Authorization header、環境変数の秘密値
- Provider/MCP token
- 不要な全文プロンプト、ユーザープロフィール、スレッド全文

ログにはサイズ上限とローテーションを設ける。ユーザーへ返す結果にも「何を実行したか」を含め、監査ログだけを見なければ分からない状態にしない。

## 費用上限

2026-10-13の費用分析を受け、1依頼あたりの費用をコードで制限する。

- 1回の `hermes chat` 内のツール反復は、生成・修正で最大10回、Reviewで最大4回とする（`generation_max_turns`、`review_max_turns` で変更可）。
- 各実行の終了後、ワーカー用Hermes homeの `state.db`（`sessions` テーブル）からそのセッションの使用量を読み、監査履歴と `kakeratta/state/usage/{requestId}.json` へ累計する。累計は再開をまたいで引き継ぐ。
- 実行の前後で累計を上限と比べる。既定はAPI呼出し40回、トークン1,500,000（通常入力＋キャッシュ読取り・書込み＋出力。reasoningは出力の内訳なので加算しない）。`max_api_calls`、`max_request_tokens` で変更可。
- 上限に達した場合、または使用量を読めない場合（セッションID不明、タイムアウト、行やテーブルの欠落）はfail-closedする。その実行の結果は捨て、以後のモデル呼出しと操作の実行を行わず、再開もせず、上限と理由を `reply_request` で返す。
- 判定は実行と実行の間で行うため、1回の実行の中では上限を超え得る。その幅はツール反復数の上限で抑える。

## 障害時

- `list_asks`、claim、Provider、Review、実行、replyのいずれかが失敗したらfail-closedする。
- claim前の失敗では取得せず、次回tickで再試行できる。
- claim後の失敗では同じclaim IDと監査履歴を使い、既実行の操作を繰り返さない。
- 実行済みでreplyだけ失敗した場合は、保存済み結果を再送し、Pythonを再実行しない。
- 例外時にSuperへフォールバックしない。

## 実装候補

- `frontend/public/integrations/hermes/kakeratta/scripts/direct_request_worker.py`を新設する。
- `check_kakeratta_gate.py`からpending requestsだけをワーカーへ渡す。
- `call_tool`を引数付きMCP呼び出しと長時間処理に対応させる。
- Gateの配布バージョンを更新し、`get_integration`でSkill、Gate、Workerを同期できるようサーバーのintegration bundleを拡張する。
- 監査状態と実行済み記録はHermes home配下へアトミック保存する。
- Token Factory呼び出しは既存のProvider v2認証を使い、キーを設定ファイルやログへ展開しない。
  - 実装: ワーカーはProviderへ直接リクエストせず、専用Hermes home（`kakeratta/hermes-home`）で単発の `hermes chat` を起動する。そのconfigはkakeratta MCPを読み取り専用ツールに限定（`tools.include`）し、ワーカー自身の読み取り専用ファイルMCPを加え、`max_tokens` は16000（推論込み。コスト抑制のため。超えた出力は途中で切れ、修正に回る）。
  - 成果物と、その保存などの副作用を同時に求める依頼は、`deliverable` に `program` を添える。programはReview合格後に1回だけ実行し、成果物は環境変数 `KAKERATTA_DELIVERABLE` のファイルから読む。返信は成果物そのもの。

## 受入条件

1. idle tickはLLMを呼ばず `wakeAgent:false`。
2. persona askだけなら従来のSuper経路が動く。
3. 直接依頼だけならSuperのAPI呼び出しが0回。
4. 生成Ultraはread/Web以外のツールを呼べない。
5. 生成とReviewは異なるセッションである。
6. 不合格時に最大2回修正でき、3回目以降へ進まない。
7. 操作PythonはReview合格かつハッシュ一致後に1回だけ実行される。
8. reply失敗後の再試行で操作を再実行しない。
9. 最終成果物または実行結果が `reply_request` で届く。
10. 監査履歴から取得、各判定、実行、返却を追跡でき、秘密値が含まれない。
11. 各実行のトークン使用量が監査履歴に残り、1依頼の呼出し数またはトークン量が上限に達するか使用量を読めない場合は、未実行のまま理由を返す。

## 関連資料

- [Hermes連携ガイド](hermes_ja.md)
- [NemoClaw統合・検証レポート](nemoclaw-hermes-integration_ja.md)
- [Hermes-4-405B実測レポート](hermes-4-405b-visual-evaluation_ja.md)
- [実装済みの外部エージェント仕様](system/integrations.md)
