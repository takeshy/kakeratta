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

トークンを設定した場合は、各ブラウザで一度 `/?token=<URLエンコードしたトークン>` を開いてください。
サーバーが HttpOnly のセッションCookieを設定し、トークンを除いたURLへ移動します。
以後のAPI呼び出しとAndroidの共有は、このCookieで認証されます。
画像は貼り付け・multipart共有とも1投稿あたり20 MiBまでです。上限を超える共有は、途中で切って保存せず拒否します。

Firebaseの3変数を設定すると、共有トークンの代わりにGoogleログインを使います。
Firebase IDトークンを署名付きHttpOnlyセッションCookieへ交換し、Firebase `uid`ごとに
保存先、反応キュー、イベント配信、音声サービスを分離します。

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

**残す位置と使う位置は別です。** 反応のために現在地が要ることと、過去の投稿に位置を残すことは別の判断で、
後者は設定で切れます（切っても反応には使われます）。

## Timeline

自分の投稿と一言、ペルソナの発言、それへの応答が時系列に並びます。反応が付いた投稿も付かなかった投稿も
同じように並びます。未読の数も「AIが考えています」も出しません。

**読み上げモード**で、移動中に溜まった反応を聞けます（ブラウザ内蔵の Web Speech API）。

## モデルと構成

![設定。段ごとのモデル、道具、位置、音声入力、読み上げ](captures/ja/settings.png)

| 用途 | 設定 | 既定 |
| --- | --- | --- |
| low の担当 | `lowModel` | Nemotron Nano |
| middle の担当 | `middleModel` | Nemotron Super |
| high の担当 | `highModel` | 未設定（middle に落ちるので、既定は実質2段） |
| 画像解析・OCR | `visionModel` | Nemotron Nano（vision） |
| 振り分け | `routeModel` | Nemotron Nano |
| 音声認識 | 音声入力の設定 | whisper.cpp / OpenAI / Gemini など |
| 読み上げ | — | ブラウザ内蔵 |

**どのモデルをどの段に置くかが、そのまま体験の差になります。** コスト最適化ではありません。
low は浅い感想でよく、「わからんくなってきた」で終わってもよい。そこに上の段が後から拾いに来ます。
一方的に賢いことばかり言われてもつまらないので、**段が本当に違うこと**が効きます。

**段は3つありますが、モデルを3つ用意する必要はありません。** 空の段は1つ下に落ちるので、
既定は実質2つ（low と middle）で動きます。high に何か置けば3段になり、全部同じにすれば1段です。
段の数だけ対比が増え、1段にすると無くなります。

- 推論は Nebius Token Factory（OpenAI互換なので接続先は差し替えられます）
- **クライアントから直接叩かず中継を置きます。** 反応の生成は非同期でサーバ側に走るため、鍵も処理も
  そちらに寄せます。Nebius Serverless Endpoints を想定
- 反応の生成は非同期（`internal/persona` のキュー）。Nebius Serverless Jobs での背景処理を想定
- 投稿データは端末をまたぐためサーバ側。いまはプロセスのディスクに置いています（`Store` の blob まわりが
  Nebius Object Storage に差し替える一点です）
- Web検索は Tavily を想定

## データ

```
<workspace>/settings.json          接続先・モデル・道具の鍵・いる担当
<workspace>/timeline.jsonl         投稿と発言（追記のみ）
<workspace>/blobs/<id>.png         キャプチャ
<workspace>/memory/<id>/           ペルソナごとのメモ（OKFバンドル。本体は中を読まない）
<workspace>/memory/<id>.meta.json  起きた回数など
<workspace>/skills/<id>/skill.json 追加した担当
```

タイムラインは**追記のみ**です。エントリを書き換えず、変更（画像の読み取りが終わった、消した）は
同じidの行を後ろに足して上書きします。追記はログがどれだけ伸びても同じコストで、書き込み途中で落ちても
失うのは最後の1行だけです。
