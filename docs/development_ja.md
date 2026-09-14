# 開発と実装状況

[README](../README_ja.md) · [English](development.md)

## 検証

```sh
task test  # Go と JavaScript のテスト
```

## いまの実装状況

実装済み: 共有シートからの受け口（PWA share_target / iOSショートカット） / 投稿（キャプチャ・ペースト・
URL・音声） / 画像の一度きりの解析 / タイムライン表示と返信 / 振り分け / ペルソナのメモと発言 /
low→middle→high の掛け合い / 位置と時刻 / ツール（Web検索・店舗検索・経路） / 読み上げ。

**入り口を最初に通しました。** ここだけが「通るか通らないか」しかないためです。ペルソナのメモ機構や
反応の閾値は、詰まっても質が下がるだけで代替が効きますが、入り口が通らなければ設計ごと変わります。
入り口が通ることは `internal/server/server_test.go` で、掛け合いの順序は
`internal/persona/runner_test.go` で押さえています。

GLM-5.3-Flashによる日本語スクリーンショットの読み取りとJSON応答は、Token Factoryの実APIで確認済みです。

未確認:

- iOS ショートカットで画像を受け取って POST する導線が実機で回るか
- Android の Web Share Target が Kindle からのスクリーンショット共有で実際に出るか
- 各担当分野におけるLightning・Super・Ultraの判断の質
- OCR 品質。特に日本語と Kindle の縦書き
- 反応の閾値。低すぎるとおせっかい、高すぎると死んでいるように見える

## 構成

```
internal/timeline   投稿と発言の一列（スレッドの切り出しと、プロンプトへの書き出し）
internal/capture    画像の一度きりの解析と、公開ページの取得
internal/persona    スキル、振り分け、反応ループ（low→middle→high）、渡すメモ形式（skills/okf）
internal/tools      宣言したものだけ呼べる道具
internal/websearch  Web検索の接続先
internal/llm        Nebius Token Factory推論クライアント（画像・ファイル入力を含む）
internal/speech     旧音声入力コード（サーバからは公開しない）
internal/store      追記ログ・画像・メモ・設定
internal/server     HTTPの表側（ページ、API、SSE）
frontend/src        ES モジュール。出荷物にバンドラは入らない
frontend/public     サイトのルートに置くもの。manifest、service worker、アイコン
frontend/lp         紹介ページ（/lp/）。日本語と英語を1つの文書に持つ
frontend/manual     マニュアル（/manual/）。アプリのヘッダーから別タブで開く
frontend/site       2ページ共通のスタイルと言語切替
frontend/stories    Storybook のストーリー。同じモジュールと、API と同じ形のフィクスチャで組む
frontend/dist       Firebase Hosting が配信するもの（上のコピー）
```

## フロントエンド

画面は単独で成立する静的サイトで、Go のバイナリは埋め込みも配信もしません。`frontend/src` は
素の ES モジュールとスタイルシート1枚で、出荷物もそのままです。`npm run build`（`node build.mjs`）
が Firebase Hosting 用に `frontend/dist` へコピーするだけで、間にバンドラは入りません。

開発では Vite をサーバーとしてだけ使います。`npm run dev`（API も一緒に立てるなら `task dev`）が
画面を :5173 で配信し、`/api` を :8787 のバイナリへ中継します。Hosting が `/api/**` を rewrite するのと
同じ分け方なので、開発と公開で違いを持たせる必要がありません。画面だけの変更はイメージに触れずに
反映できます。

`npm run storybook`（`task stories`）で Storybook が開きます。`frontend/stories` のストーリーは
本物のモジュール（`createRoom`・`createCompose`・`createFeed`・`createPersonas`・`createSettings`、
`screenNodes` が組む画面全体と `sheetNodes` のシート）を、API が返すのと同じ形のフィクスチャで描くので、
そこで見ているものが出荷物そのものです。パネルは自分で `/api` を読むため、そのストーリーでは
フィクスチャから答える `fetch` を渡します（`stories/api.js`）。文言は同じ元から両言語を出しているため、ツールバーで表示言語を切り替えられます。
Vite と Storybook は開発依存だけで、`dist/` には入りません。

README のキャプチャ（`docs/captures/{en,ja}/`）は、このストーリーから `npm run captures`
（`task captures`）で撮ります。Storybook を立て、決めたストーリーを両言語で headless Chromium が
撮影し、`docs/captures/` に置きます。見え方が変わったら撮り直してください。

音声入力は speech-popup 由来のブラウザ認識に統一しています。録音APIとライブAPIは公開していません。終了フレーズによる自動送信では、任意でlowモデルによる整形を保存前に行います。手動送信では整形しません。回帰テストは `node --test tests/*.test.js` と `go test ./...` で実行します。

## ネイティブを作るとしたら

現時点では不要です。将来寄せる場合、iOS は Share Extension（別ターゲット、App Group または直接 POST）、
Android は `ACTION_SEND` の intent-filter になります。器は Wails v3 で兼ねられますが、モバイル対応は
公式に実験的とされており、デスクトップのベータ互換性の保証外です。いまは範囲に入れていません。

## Bluesky連携とキャプチャ

Go 1.26以降が必要です。`internal/social` がOAuth・投稿計画・公開を担当し、`frontend/src/bluesky.js` と `social.js` が接続と発言選択・プレビューを表示します。APIを模擬したテストを用意しています。実アカウントでの認可と投稿は未確認です。

`frontend/stories/Bluesky.stories.js` は接続・選択・プレビューの実画面をサンプルデータで表示します。外部への認可や投稿は行いません。日英の関連画像だけを撮り直す場合:

```sh
CAPTURE_ONLY=bluesky-connect,bluesky-selection,bluesky-preview,settings,timeline,search-thread npm --prefix frontend run captures
npm --prefix frontend run build
```

## ブラウザ保存とオフライン検証

`local-store.js` が利用者別のIndexedDB保存、`outbox.js` が送信待ちを扱います。保存する会話は直近20件の元投稿とその返信です。未送信は別枠で保持し、サーバーの `CreateEntry` が同じ投稿IDの作成を原子的に一度に制限します。Service Workerは公開された画面ファイルだけをキャッシュし、APIレスポンスは保存しません。

次の実ブラウザ検証はローカルの模擬APIを使い、オフラインでの起動・投稿・再読み込み・復帰後の一回だけの送信を確認します。日英の `docs/captures/*/offline.png` も更新します。Node.js 22以降とChromiumが必要です（`CHROMIUM` で実行ファイルを指定可能）。

```sh
npm --prefix frontend run build
npm --prefix frontend run test:offline
npm --prefix frontend run build
```

## 保存時の検索索引登録

`internal/server/index.go` が投稿・担当の発言・画像解析更新のイベントを受け、200 msの間に届いた変更をまとめて `search.IndexPending` を呼びます。起動時・AI設定保存後にも補完し、失敗は30秒後に再試行します。終了時は処理をキャンセルし、Firestoreの未処理フラグと保存済みベクトルを再起動後に再利用します。Cloud Runは既存の `cpu_idle = false`・最小1インスタンス設定でバックグラウンド実行できます。検索からも同じ索引処理を使います。

検索しないままの投稿保存・画像解析完了後のベクトル化、再起動時の再利用、ひらがな・カタカナでのOCR検索をテストします。
