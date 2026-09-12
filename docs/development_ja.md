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

未確認:

- iOS ショートカットで画像を受け取って POST する導線が実機で回るか
- Android の Web Share Target が Kindle からのスクリーンショット共有で実際に出るか
- Token Factory の vision 対応と、Nemotron の各サイズで何が使えるか
- OCR 品質。特に日本語と Kindle の縦書き
- 反応の閾値。低すぎるとおせっかい、高すぎると死んでいるように見える

## 構成

```
internal/timeline   投稿と発言の一列（スレッドの切り出しと、プロンプトへの書き出し）
internal/capture    画像の一度きりの解析と、公開ページの取得
internal/persona    スキル、振り分け、反応ループ（low→middle→high）、渡すメモ形式（skills/okf）
internal/tools      宣言したものだけ呼べる道具
internal/websearch  Web検索の接続先
internal/llm        OpenAI互換クライアント（画像・ファイル入力を含む）
internal/speech     音声入力のバックエンド（鍵をサーバ側に置くための中継）
internal/store      追記ログ・画像・メモ・設定
internal/server     HTTPの表側（ページ、API、SSE）
frontend/src        バンドラなしの ES モジュール
```

音声入力は speech-popup（gemihub-desktop の音声機能）の移植をそのまま持ち込んでいます。
方式は3つ（ブラウザ音声認識 / 録音して送る音声認識API / ライブ文字起こし）。移植元のテストも
`tests/` に置き、アプリの回帰テストも追加しています（`node --test tests/*.test.js`）。

## ネイティブを作るとしたら

現時点では不要です。将来寄せる場合、iOS は Share Extension（別ターゲット、App Group または直接 POST）、
Android は `ACTION_SEND` の intent-filter になります。器は Wails v3 で兼ねられますが、モバイル対応は
公式に実験的とされており、デスクトップのベータ互換性の保証外です。いまは範囲に入れていません。
