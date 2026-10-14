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

記録禁止:

- APIキー、Authorization header、環境変数の秘密値
- Provider/MCP token
- 不要な全文プロンプト、ユーザープロフィール、スレッド全文

ログにはサイズ上限とローテーションを設ける。ユーザーへ返す結果にも「何を実行したか」を含め、監査ログだけを見なければ分からない状態にしない。

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
  - 実装: ワーカーはProviderへ直接リクエストせず、専用Hermes home（`kakeratta/hermes-home`）で単発の `hermes chat` を起動する。そのconfigはkakeratta MCPを読み取り専用ツールに限定（`tools.include`）し、ワーカー自身の読み取り専用ファイルMCPを加え、`max_tokens` は64000。
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

## 関連資料

- [Hermes連携ガイド](hermes_ja.md)
- [NemoClaw統合・検証レポート](nemoclaw-hermes-integration_ja.md)
- [Hermes-4-405B実測レポート](hermes-4-405b-visual-evaluation_ja.md)
- [実装済みの外部エージェント仕様](system/integrations.md)
