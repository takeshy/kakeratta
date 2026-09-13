# 使い方と設定

[README](../README_ja.md) · [English](usage.md)

## 動かす

```sh
task setup      # 初回のみ。画面側の開発道具を npm ci
task dev        # API（http://127.0.0.1:8787）と画面（http://127.0.0.1:5173）を同時に
task run        # API だけ
task build      # API だけビルド（bin/kakeratta）
task stories    # Storybook（http://127.0.0.1:6006）
task test       # go test ./internal/... と node --test tests/*.test.js
```

| 環境変数 | 既定 | |
| --- | --- | --- |
| `KAKERATTA_ADDR` | `:8787` | 待ち受け |
| `KAKERATTA_HOME` | OS の設定ディレクトリ | 投稿・メモ・画像の保存先 |
| `KAKERATTA_TOKEN` | 空 | 空だと誰でも入れます。**外から届く場所に置くときは必ず設定してください**（推論の鍵を持っているのはこのプロセスです） |
| `KAKERATTA_FIREBASE_PROJECT_ID` | 空 | Googleログインとユーザー別ワークスペースを有効化 |
| `KAKERATTA_FIREBASE_API_KEY` | 空 | Firebase Web APIキー |
| `KAKERATTA_FIREBASE_AUTH_DOMAIN` | 空 | Firebase Authドメイン |
| `KAKERATTA_MCP_SERVERS` | 空 | MCPサーバーの一覧（JSON）: `[{"name":"geo","url":"https://…/mcp","headers":{…}}]`。道具は `名前.道具名` で現れます |
| `KAKERATTA_NOMINATIM_URL`・`KAKERATTA_OVERPASS_URL`・`KAKERATTA_OSRM_CAR_URL`・`KAKERATTA_OSRM_FOOT_URL`・`KAKERATTA_OSRM_BIKE_URL` | OpenStreetMapの公開サービス | `place_search` と `route` の接続先 |

道具の接続先とMCPサーバーは運用者の設定で、全利用者で共通です。`web_search` のTavilyのキーは利用者自身が設定画面で入れます。

トークンを設定した場合は、各ブラウザで一度 `/?token=<URLエンコードしたトークン>` を開いてください。
サーバーが HttpOnly のセッションCookieを設定し、トークンを除いたURLへ移動します。
以後のAPI呼び出しとAndroidの共有は、このCookieで認証されます。
画像は貼り付け・multipart共有とも1投稿あたり20 MiBまでです。上限を超える共有は、途中で切って保存せず拒否します。

Firebaseの3変数を設定すると、共有トークンの代わりにGoogleログインを使います。
Firebase IDトークンを署名付きHttpOnlyセッションCookieへ交換し、Firebase `uid`ごとに
保存先、反応キュー、イベント配信を分離します。

## 紹介ページとマニュアル

アプリと同じホストに、紹介ページ `/lp/` と使い方をまとめた `/manual/` があります。README のキャプチャを使い、
日本語と英語を1つの文書に持ち、ヘッダーで切り替えます。選んだ言語はアプリの表示言語と同じブラウザのキーに保存されるので、
アプリ・紹介ページ・マニュアルで言語が揃います。URL の `?lang=ja` / `?lang=en` が最優先なので、リンクに言語を載せられます。
アプリのヘッダーの **?** アイコンを押すと、いまの表示言語でマニュアルが別タブに開きます。

ページは `frontend/lp/`、`frontend/manual/`、`frontend/site/`（共通スタイルと言語切替）の素の HTML です。
`node build.mjs` が `frontend/dist` へコピーし、`docs/captures` も `/captures/` として一緒に置きます。
開発中は Vite が `docs/` のキャプチャをそのまま配信します。

## 入り口

思いついたことは書くのが早いし、画面に出ているものは撮るのが早い。どれも同じ1つの欄から入ります。

| 入り口 | 中身 |
| --- | --- |
| 書く | 一言。思いついたことはこれが一番速い |
| 撮る・貼る | 画像（Ctrl+V で貼る、共有シートから渡す、ファイルを選ぶ） |
| URL | 公開ページの本文を取りに行きます |
| 話す | 書き起こした文章。送信の合図で確定します |

画像が強いのは、**ログインの有無もアプリの種類も問わない**ところです。Kindle も有料記事も SNS も、
画面に出ていれば撮れます。API も DRM も経由しません。読書中に気になった一節を撮って一言つける、
というのはこれでしか成り立ちません。

スマホから撮って渡す導線だけ、器の用意が要ります。

| | 撮って渡す | 器 |
| --- | --- | --- |
| PC | スクリーンショットがクリップボードに乗る → 貼る（Ctrl+V） | 不要（ブラウザ） |
| Android | PWA + Web Share Target で共有シートに並ぶ | 不要（PWA） |
| iOS | ショートカット（共有シートで画像を受け取り POST） | 不要 |

### Android

ホーム画面に追加すると、`manifest.webmanifest` の `share_target` によって OS が共有先として認識します。
共有シートにネイティブアプリと同じように並び、そこから画像を渡すとタイムラインに入ります。
`sw.js` が共有のPOSTを受け取り、そのまま `/api/capture` に流します。

### iOS

Web Share Target が使えないため、ショートカットで代替します。ネイティブコードは書きません。

1. ショートカットを新規作成し、「共有シートで表示」を有効にして、受け取る種類を「イメージ」にする
2. 「URLの内容を取得」— メソッド `POST`、本文は `フォーム`
   - `image` … ショートカットの入力
   - `text` … （任意）その場で書くなら「入力を要求」の結果
   - ヘッダ `Authorization: Bearer <KAKERATTA_TOKEN>`

読書中は無言で投げて、あとからまとめて一言を足す使い方もできます。

`/api/capture` は multipart を受けるのでサーバ側に置く必要があります。静的ホスティングでは受けられません。

## 投稿に添えるもの

**「今どこで何時か」だけを渡します。** 目的地や目的時刻は発言の中にあります（「20時に渋谷に着きたい」）。
それはペルソナが文面から読み取ります。

**場所は任意です。** PCからの投稿には意味のある位置がありません。無くても成立します。

位置が取得できた場合は投稿に保存し、反応にも使います。

## Timeline

自分の投稿と一言、ペルソナの発言、それへの応答が時系列に並びます。反応が付いた投稿も付かなかった投稿も
同じように並びます。未読の数も「AIが考えています」も出しません。

最新20投稿から表示し、下へスクロールすると20投稿ずつ追加します。投稿への発言と返信は「返信 N件」の下にたたまれ、
「一言足す」「返す」を押すとそのスレッドが開きます。反応は通常ライブ接続で届きますが、取りこぼしに備えて、
投稿・返信を送ってから5分間だけ10秒ごとにタイムラインを読み直します（タブが裏にある間は読み直しません）。

**読み上げモード**で、移動中に溜まった反応を聞けます（ブラウザ内蔵の Web Speech API）。

## 検索

ヘッダーの「検索」と、担当が使う `timeline_search` ツールは同じ仕組みを共有します。対象は本文・URL・画像の読み取り結果・場所名・返信で、
削除した投稿とその返信は結果に出ません。

- **キーワード検索**: 空白で区切ったすべての語を含むかけらを、新しい順に返します。大文字・小文字、全角・半角の違いは無視します。
  保存したかけらを毎回すべて読むので、投稿が増えるほど読み取り量も増えます。
- **意味検索**: `Qwen/Qwen3-Embedding-8B`（1024次元）で文章をベクトルにし、意味が近い順に並べます。設定済みのAI接続先とAPIキーを使うので、
  接続先がこのモデルの embeddings API を提供している必要があります。

意味検索の準備（索引づくり）は、初回と接続先を変えたときに既存のかけらすべてで行い、以降は追加・変更されたかけらだけを次の検索時に処理します。
準備は一度にひとつだけ走ります。担当の検索が準備中のときに画面から検索すると、数秒待ったあと「混んでいます」と表示されるので、少し待ってからもう一度検索してください。
準備が時間切れになっても、進んだ分は保存され、次の検索で続きから再開します。

保存先:

- `KAKERATTA_STORE=firestore`: ユーザー配下の `searchPassages` にベクトルを保存し、Firestore のベクトル検索（`FindNearest`、コサイン距離）で近い100箇所を取り出して投稿ごとにまとめます。
  `firestore.indexes.json` のベクトルインデックスが必要です（[デプロイ手順](../infra/README_ja.md#firestoreのインデックス)）。
- `KAKERATTA_STORE=file`: `<workspace>/search-embeddings/` にベクトルを保存し、類似度はGoで計算します。

## 音声入力

音声入力はブラウザ認識を使用します。設定の「自動送信前にAIで文章を整える」は初期状態でOFFです。ONにすると、終了フレーズによる自動送信時だけlowモデル（既定はNemotron Nano）が句読点や言いよどみを整えてから投稿します。返信にも適用します。手動送信と無音停止では整形しません。整形に失敗したときは投稿せず、入力を残します。

## モデルと構成

KakerattaはNebius Token Factoryを通じてNVIDIA Nemotronモデルを使用します。

- Nemotron Nano — 軽量な振り分けと、日常的な担当の判断
- Nemotron Super — 担当分野をより深く考える推論
- Nemotron Ultra — 担当が必要とする複雑な推論
- Nebius Token Factory — 推論基盤

画像の読み取りには、同じ基盤のGLM-5.3-Flashを使用します。

![設定。表示言語、モデル、Web検索、音声入力、読み上げ](captures/ja/settings.png)

| 用途 | 設定 | 既定 |
| --- | --- | --- |
| low の担当 | `lowModel` | Nemotron Nano |
| middle の担当 | `middleModel` | Nemotron Super |
| high の担当 | `highModel` | Nemotron Ultra |
| 画像解析・OCR | `visionModel` | GLM-5.3-Flash |
| 振り分け | `routeModel` | Nemotron Nano |
| 音声認識 | 音声入力の設定 | ブラウザ音声認識 |
| 読み上げ | — | ブラウザ内蔵 |

画面にモデルの選択欄はありません。利用者はAPIキーを入れるだけで、段ごとのモデルは上の既定のままです。
ワークスペースのモデルを変えたいときは、その `settings.json` の上記キー（または `PUT /api/settings`）を
運用側で書き換えます。画面は保存されている値のまま動きます。

**どのモデルをどの段に置くかが、そのまま体験の差になります。** コスト最適化ではありません。
low は浅い感想でよく、「わからんくなってきた」で終わってもよい。そこに上の段が後から拾いに来ます。
一方的に賢いことばかり言われてもつまらないので、**段が本当に違うこと**が効きます。

**既定はNano → Super → Ultraの3段です。** Token Factoryではhighが空欄の場合もUltraを使います。
各担当に段を指定し、highの担当は先に動いた担当の発言も踏まえて、複雑な内容を考えます。

- 推論基盤はNebius Token Factoryです。Nemotronによる推論とGLMによる画像読み取りに使います。
- ブラウザからGo APIへリクエストを送り、APIキーの保持と非同期の反応処理はサーバー側で行います。
- 公開環境ではAPIをCloud Run、画面をFirebase Hostingで配信し、利用者のデータをFirestoreとCloud Storageに保存します。ローカル環境ではディスクに保存します。
- Web検索はTavilyです。キーは利用者ごとに入れます。

## データ

```
<workspace>/settings.json          接続先・モデル・Web検索の鍵・いる担当
<workspace>/timeline.jsonl         投稿と発言（追記のみ）
<workspace>/blobs/<id>.png         キャプチャ
<workspace>/memory/<id>/           ペルソナごとのメモ（OKFバンドル。本体は中を読まない）
<workspace>/memory/<id>.meta.json  起きた回数など
<workspace>/skills/<id>/skill.json 追加した担当
<workspace>/search-embeddings/     意味検索のベクトル（消しても次の検索で作り直します）
```

タイムラインは**追記のみ**です。エントリを書き換えず、変更（画像の読み取りが終わった、消した）は
同じidの行を後ろに足して上書きします。追記はログがどれだけ伸びても同じコストで、書き込み途中で落ちても
失うのは最後の1行だけです。
