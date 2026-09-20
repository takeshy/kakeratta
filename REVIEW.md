# アプリ全体レビュー（2026-09-12）

README.md / README_ja.md を基準に、投稿・共有・タイムライン・振り分け・段階実行・メモ・設定・認証・検索ツール・音声入力・読み上げを確認した。作業開始時からあった6ファイルの変更は保持した。

## 修正した指摘

| 優先度 | 問題と影響 | 修正箇所 |
| --- | --- | --- |
| P1 | 録音型の音声認識が保存済みキーの伏せ字を送信し、認証に失敗する | `internal/speech/http_proxy.go`：設定した宛先・POSTメソッドを照合してからサーバーのキーを付与 |
| P1 | 画像解析中に削除すると、古い投稿の書き戻しで復活する | `internal/store/store.go`、`internal/persona/runner.go`：最新の削除状態をロック内で確認して解析結果を追記 |
| P1 | クラッシュによる改行なしの末尾に次の投稿が連結し、新しい投稿まで読めなくなる | `internal/store/store.go`：末尾の区切りを復旧してから追記。読み取り上限を超えるレコードも保存前に拒否 |
| P1 | multipartの上限がメモリ使用量にしか効かず、巨大画像が途中で切られて保存される | `internal/server/guard.go`、`routes.go`：認証前に本文サイズを制限し、画像上限超過を拒否、一時ファイルを片付ける |
| P1 | URL取得の事前DNS検査と実際の接続先が変わると、内部ネットワークへ接続できる | `internal/capture/fetch.go`：接続時のIPも検査。公開ページ取得では環境プロキシを使用しない |
| P1 | Android共有後の相対URLによる `Response.redirect` が例外になる | `frontend/sw.js`：絶対URLを使用。失敗時は画面に通知 |
| P2 | 振り分けが `{}` / `null` / 不明IDを返すと、投稿が誰にも渡らない | `internal/persona/route.go`：不正な判定は全員へ。明示的な空配列は無反応を維持 |
| P2 | スクリプト・CSSがURL本文に混ざる。共有URLでは本文取得自体が行われない | `internal/capture/fetch.go`、`internal/server/routes.go`：非本文要素を除外し、通常投稿と共有で取得処理を共通化 |
| P2 | 設定保存で空欄のモデルが実効値に置き換わり、下位モデルへの追従が失われる | `internal/store/settings.go`：編集用の設定と実行用の設定を分離 |
| P2 | 20 MiB以下の貼り付け画像でもJSON本文の8 MiB制限で投稿できない | `internal/server/routes.go`：Base64分の増加を考慮。録音送信の本文上限も調整 |
| P2 | 削除済み投稿の返信がAIに渡る、または削除済み宛先への返信を受理する | `internal/timeline/timeline.go`、`internal/server/routes.go`：削除したスレッドを除外し、返信先を確認 |
| P2 | 投稿・検索結果のURLをそのままリンクにできる | `frontend/src/dom.js`：HTTP(S)以外のリンクを除外 |

## 検証と残る確認

検証結果：`go test -race ./...` 成功、`node --test --test-isolation=none tests/*.test.js` は79件成功、`node frontend/build.mjs` と `go build -o bin/kakeratta .` 成功。API・モデルの通信は主にテスト用の応答で確認しているため、外部プロバイダの実際の認証・推論品質、iOS/Androidの共有実機動作、ブラウザのマイク・読み上げの実機動作は未検証。

OpenCode接続では `internal/llm/opencode.go` が画像添付を送信していないため、画像解析用には別の対応接続先が必要。OpenCodeの添付対応は今回未修正で、接続先との結合テストが必要。

今後の改善候補：反応キューはメモリ上にあり、プロセス異常終了時の未処理分を再開できない。メモ一覧・個別読取が全メモの読み込みを伴い、メモの上書きは一時ファイルを使わないため、長期運用での性能とクラッシュ耐性は追加検討が必要。複数端末や設定の連続操作による更新競合にも、バージョン管理や更新の直列化を導入する余地がある。
