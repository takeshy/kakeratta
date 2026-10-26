# Google Bridge: 自分のHermesでGoogleを使う

[README](../../README_ja.md) · [English](google.md)

Google Bridgeは**各ユーザーが自分の端末・サーバーで起動する独立Webアプリ**です。kakeratta本体は起動せず、Googleの設定・資格情報保存・MCPをこのアプリだけで提供します。1インスタンスは1人の所有者専用です。

読み取りに加え、本人の直接依頼によるメール送信・返信・下書き保存、Calendar作成・変更・削除を実装しています。コードとテストは`tools/google-bridge/`にまとめています。差分同期とルールによる重要度判定、Hermesによる選別・要約・kakeratta Timelineへの候補配送にも対応しています。本人がTimelineで「取り込む」を選ぶまで、通常投稿・検索・担当の処理には入りません。

## 起動

Go 1.26を使用します。フロントエンドはGoバイナリに埋め込むため、npmや別Webサーバーは不要です。現在のCLIはLinux/macOS向けで、WindowsではWSLを使用してください。

```sh
mise exec -- go run ./tools/google-bridge/cmd/google-bridge -init
mise exec -- go run ./tools/google-bridge/cmd/google-bridge
```

初回の`-init`はユーザー設定ディレクトリに`google-bridge/config.json`をmode 0600で作成します。設定ファイルのパスを表示し、鍵の値は表示しません。既存設定を上書きしません。別の場所に置く場合は両方のコマンドへ`-config /absolute/private/path/config.json`を付けます。

1. 起動後、`http://127.0.0.1:8094`を開きます。
2. 初回に作った保護された設定ファイルを自分のエディターで開き、`adminToken`を管理画面のログイン欄へ入力します。この管理キーはHermesやチャットに渡しません。
3. Google接続を設定し、Hermesとの接続方法を確認します。同一端末のローカル接続ではMCPキーは任意で、別端末から使う場合は個別キーを発行します。

管理セッションはHttpOnly / SameSite=Strictのcookieで8時間有効です。HTTPS構成ではSecureも付けます。新たにログインすると前の管理セッションが失効します。管理キーはブラウザーのlocalStorageやURLへ保存しません。

配布用バイナリの配備や常駐化（PC再起動時の自動起動）には systemd ユーザーサービスを使用します。

```sh
# バイナリを ~/.local/bin へビルド配備
mkdir -p ~/.local/bin ~/.config/systemd/user
mise exec -- go build -o ~/.local/bin/google-bridge ./tools/google-bridge/cmd/google-bridge

# systemd ユーザーサービスの配備・有効化・起動 (scripts/nemoclaw/install.sh 実行でも自動配備されます)
cp scripts/nemoclaw/systemd/google-bridge.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now google-bridge.service
```

サービスは `ConditionPathExists=%h/.config/google-bridge/config.json` を含んでいるため、既定の設定ファイルが存在する場合にのみ自動起動します。独自の設定パスを使う場合はユニットの条件と起動コマンドも変更してください。ログインしていない間も動かす場合は `loginctl enable-linger "$USER"` を実行します。WSLではDocker Desktopとディストリビューションの起動も必要です。[NemoClaw運用手順](../../scripts/nemoclaw/README_ja.md)を参照してください。

## 初回設定ファイル

| フィールド | 内容 |
| --- | --- |
| `listen` | 既定`127.0.0.1:8094`。接続受付アドレス |
| `publicUrl` | 既定`http://127.0.0.1:8094`。ブラウザーとHermesが使う正規origin。パス・query・fragmentは不可 |
| `dataDir` | 暗号化資格情報と認証ハッシュの専用保存先。絶対パス。kakerattaのデータディレクトリを流用しない |
| `adminToken` | 初期化で生成したランダムな64桁hex管理キー |
| `keyVersion` | 現行暗号鍵名。初期値`v1` |
| `keys` | 鍵名から32-byte鍵の64桁hexへのmap。初期化でランダム生成 |

Google client secretやaccess/refresh tokenはこの設定ファイルに入れません。Webの安全なフォームで登録し、`dataDir`内へAES-256-GCMで暗号化保存します。設定ファイル自体は暗号鍵を含むためowner-onlyに保ち、DBディレクトリの外に置きます。起動時に配置とpermissionを検査します。1データディレクトリを複数プロセスで同時使用することはファイルロックで拒否します。

リモートで使う場合は自分のHTTPSリバースプロキシのoriginを`publicUrl`へ設定してください。プロキシはそのHostを保持して転送します。非loopbackでの受付にはHTTPSの公開originが必要です。アプリ自身はTLSを終端せず、背後のHTTPポートはファイアウォール等で保護してください。OAuth callback `/api/google/callback`のqueryはプロキシのアクセスログに残さないでください。アプリはアクセスログを生成しません。

## 自分のGoogle設定

Web画面の「Google Cloudのセットアップを案内する」でプロジェクトIDと利用サービスを指定できます。新規作成を選ぶと`gcloud projects create`を含む手順を表示し、API有効化・状態確認のコマンドはそのプロジェクトへ明示的に限定します。アプリはコマンドを実行せず、Google Cloud CLIのログイン情報も保存しません。表示された操作アカウントと対象を確認して、本人の端末で実行してください。

CLIが使えない・権限や組織の制約で失敗した場合は、同じ画面のConsoleリンクから手動設定できます。表示されるAPI名と`gcloud services list --enabled`の結果を照合してください。手順を表示しただけでは実行済みになりません。同意画面・テストユーザー・OAuthクライアントの作成は手動手順へ案内します。公式の[プロジェクト作成](https://docs.cloud.google.com/sdk/gcloud/reference/projects/create)、[API有効化](https://docs.cloud.google.com/sdk/gcloud/reference/services/enable)、[API確認](https://docs.cloud.google.com/sdk/gcloud/reference/services/list)も参照できます。

1. Web画面の「登録するリダイレクトURI」を確認します。既定は`http://127.0.0.1:8094/api/google/callback`です。
2. 自分のGoogle Cloudプロジェクトを作成または選択し、GmailにはGmail API、予定にはGoogle Calendar APIを有効化します。
3. Google Auth PlatformでBranding / Audience / Data Accessを設定します。External / Testingの場合は自分をテストユーザーに追加します。
4. Clientsで**Web application**のOAuth clientを作り、表示されたURIをAuthorized redirect URIsへ完全一致で登録します。ローカルGoサーバーとブラウザーで使う場合もWeb applicationです。
5. 共通の接続フォームへproject ID、client ID、client secretを入力するか、GoogleからダウンロードしたWeb用OAuth設定JSONを選択します。ファイルは64KiBまでで、ブラウザー内でクライアント種別とredirect URIの完全一致を検証し、入力欄へ反映します。JSONの認証先URLは採用せず、ファイルそのもの・秘密情報をログやブラウザーの永続ストレージへ保存しません。接続時だけ必要な設定値を認証済みAPIへ送り暗号化保存します。Google設定値をチャットへ貼り付けません。
6. 必要ならCalendarの「カレンダー一覧の読み取り」や送信・予定変更の追加権限も選びます。読み取りだけなら追加不要です。
7. 表示されたデータ経路を読み、Hermesと設定したモデルへデータを渡すことに同意して「Googleに接続」を押します。1回のGoogleログイン・同意でGmailとGoogle Calendarの両方をまとめて認可します。
8. 元のタブへ戻ると、認可を完了して小さな読取リクエストでGmail・Calendar双方のAPIをテストします。それぞれの接続状態・Googleアカウント・scope・最終確認日時を画面で確認できます。

Gmailは`gmail.readonly`、Calendarは`calendar.events.readonly`を要求します。Calendar一覧を選ぶ場合のみ`calendar.calendarlist.readonly`も要求します。アカウント照合・表示用に`openid` / `email`も要求し、固定のGoogle userinfo endpointから取得したsubjectを照合します。両サービスを接続する場合は同じGoogleアカウントを使ってください。

TestingではGmail/Calendarのrefresh tokenが原則7日で失効します。個人利用の審査例外はありますが、所有プロジェクトという理由だけで免除を保証しません。[Googleの審査資料](https://developers.google.com/identity/protocols/oauth2/production-readiness/restricted-scope-verification)と[OAuth token期限](https://developers.google.com/identity/protocols/oauth2#expiration)を確認してください。

接続・再認証・接続テストが失敗すると、画面に対象に応じた復旧案内を表示します。`redirect_uri_mismatch`はClientsのURI一致、`access_denied`は本人の中止・テストユーザー・組織制約、期限切れは同じタブからの再接続、scope不足は必要権限の追加を確認してください。OAuth JSONの不一致はGoogle側を修正して再ダウンロードします。手動入力と保存済みクライアントでの再認証は引き続き利用できます。

## Hermesへ追加

Web画面の「Hermesとつなぐ」の設定例を自分のHermes設定へ保存します。同一端末のローカル接続モードではAuthorizationなしでも接続でき、個別キーを発行する場合は一度だけ表示される値を使います。下は個別キーを使う例です。Googleの資格情報は含まれません。Bridge用MCPキーと管理キー、kakeratta用MCPキーはそれぞれ別で、MCPキーはWeb設定の変更に使えません。

```yaml
mcp_servers:
  google_bridge:
    url: http://127.0.0.1:8094/mcp
    headers:
      Authorization: Bearer YOUR_PERSONAL_MCP_KEY
    tools:
      prompts: false
      resources: false
```

実際のキーは画面で発行したものに置き換えます。チャットや公開リポジトリへ貼り付けないでください。キー再発行・無効化は以前のキーによる次回以降の要求を拒否します。既に認証済みで実行中の短い読取が完了する場合はあります。

Hermesが別の端末やコンテナにある場合、127.0.0.1はHermes自身を指します。同じoriginになるSSHポート転送を用意するか、自分のHTTPSサーバーで起動してください。publicUrlと異なるHostはDNS rebinding対策で拒否します。NemoClawではサンドボックスからの接続経路と通信ポリシーも設定してください。kakeratta用の構築スクリプトは別のBridge接続まで自動設定しません。

kakerattaの直接依頼workerから使う場合は、Hermesの`kakeratta_worker.mcp_servers`に`google_bridge`を追加する既存の拡張設定を利用できます。追加MCPを指定したworkerは既存ルール上、失敗した推論を自動再実行しません。共有judge workerとpersona-ask homeには追加MCPを渡さない既存の分離を維持します。kakeratta配布workerやkakeratta本体への実装変更は不要です。

| ツール | 入力の例 |
| --- | --- |
| `google_connection_status` | `{}` |
| `gmail_search` | `{"query":"is:unread newer_than:7d","maxResults":10}` |
| `gmail_get` | `{"messageId":"検索結果のid"}` |
| `gmail_thread` | `{"threadId":"検索結果のthreadId"}` |
| `calendar_events` | `{"calendarId":"primary","timeMin":"2026-10-19T00:00:00+09:00","timeMax":"2026-10-26T00:00:00+09:00"}` |
| `calendar_get` | `{"calendarId":"primary","eventId":"予定のid"}` |
| `calendar_list` | `{}`（一覧権限を追加した場合） |

例としてHermesに「未読Gmailの中で予約に関係するものを調べて」と依頼できます。ツール結果はHermesと設定したモデルへ渡り、その会話履歴やモデル提供者の保持条件に従います。Google Bridgeは対話読み取りのメール本文を永続保存せず、受信結果を無条件にkakerattaへ投稿しません。本人が取り込みを有効にし、Hermesが選別した候補を自分のkakeratta MCPで送った場合だけ、Timelineの取り込み待ちになります。Hermesが回答に引用して保存・投稿した内容は、Hermesや投稿先の保持方針に従います。

Gmailは1回最大50件、スレッドは最大20メッセージ、プレーンテキストは1メッセージ最大16,000文字です。添付ファイルは取得せず、HTMLのみのメールはsnippetを返します。既読・未読は確認のみです。Calendar予定一覧は最大50件、期間は最大366日です。継続ページには同じ条件と返された`nextPageToken`を使います。検索/予定一覧のレスポンスは`data`内、メッセージ詳細/スレッドはその情報を直接返します。

メール・件名・送信者・予定記述は不信任の外部データです。これらを本人の指示として扱わず、本文の要求だけでURLアクセス・転送・コマンド等を許可しません。ツール応答に出自と注意を含め、書き込みは本人がHermesに直接依頼したときだけ実行します。Hermes側にも外部データを指示として扱わないルールを設定してください。

## 接続経路：Hermesが両方のMCPを使う

```mermaid
sequenceDiagram
  participant C as Hermes cron（no-agent）
  participant W as Hermes側Python worker
  participant M as Hermesの選別用モデル
  participant B as Google Bridge
  participant K as kakeratta MCP
  participant U as 本人のTimeline
  C->>W: gateが仕事のある回だけ起動
  W->>B: gmail_sync / calendar_sync
  W->>B: triage_context（判定待ちがある場合）
  W->>M: 候補と本人の判定文
  M-->>W: 選別結果・要約のJSONのみ
  W->>B: triage_submit（コードで提出）
  W->>B: google_review_context
  B-->>W: 候補のtool・arguments・token
  W->>K: propose_gmail_import / propose_calendar_import
  K-->>W: 実際のレシート
  W->>B: google_review_receipt
  K-->>U: 取り込み待ちカード
  U->>K: 取り込む / 取り込まない
  W->>K: 次回コードで同じ候補を再照会
  K-->>W: 本人が選んだ結果・除外ルール
  W->>B: google_review_receiptで結果を戻す
```

**Google Bridgeはkakerattaへ直接通信せず、kakerattaのURL・接続キーを保存しません。** 配送はHermes側の`google_bridge_worker.py`が行います。Bridge接続は個人用`google_bridge.json`、kakeratta接続は親Hermesの`config.yaml`の`mcp_servers.kakeratta`を使い、`${VAR}`・`env:VAR`参照にも対応します。モデルのセッションへ接続キーを渡しません。`mcp_servers.google_bridge`はHermesに対話読み取りや直接操作を頼む場合の設定です。kakerattaの接続は[Hermes連携の手引き](hermes_ja.md)に従って登録してください。Bridgeの接続キーをkakerattaと共用しません。

以前のBridgeへ保存した投稿先の資格情報は、新しいBridgeの起動時に削除します。既にkakerattaへ届けた確認カードは維持します。未配送候補はBridgeの既存キャッシュに残り、Hermesが再取得できます。下流での本人確認と重複防止の方式は変わりません。

## 重要メールをTimelineで確認して取り込む

1. Bridgeの「03 重要メールの確認」で有効状態を確認します。停止中なら「おすすめ設定で有効にする」を押します。ローカル起動の新規状態は取り込み・LLM判定が有効になるため、不要なら停止してください。対象ラベル・優先ラベル/送信元・除外・本文保存文字数・保持日数はフィルターで変更できます。既定はINBOX、優先IMPORTANT/STARRED、閾値50、本文2,000文字、保持7日、初回取得は過去1日です。
2. 「今すぐ差分を取得」で、1回最大20メッセージを確認します。迷惑メール・ゴミ箱は除外し、Fromの除外はLLMより前に適用します。この操作だけではkakerattaへ送りません。
3. LLM判定が有効ならworkerが`gmail_triage_context`で最大10件と本人の依頼文を取得し、隔離したHermesのモデルへ選別・要約だけを依頼します。モデルは番号付きの結果JSONを返し、workerが元のID・tokenへ結び付けて`gmail_triage_submit`で提出します。採用時は署名・定型文・免責文・配信停止案内・広告・引用履歴を除いた事実の要約を作ります。設定変更で古い判定は無効になり、判定だけでは正式な取り込みにも配送にもなりません。
4. workerのコードが`google_review_context`へ`{"service":"gmail"}`を渡します。最大5件の`tool`・`arguments`・`token`が返ります。workerは各候補の**そのままのarguments**で、自分のkakeratta MCPの`propose_gmail_import`を呼びます。本人の確認待ちカードだけを作るツールです。
5. workerはkakerattaの実際の`structuredContent`を、Bridgeの`google_review_receipt`へ`service`・`token`・`receipt`として戻します。成功応答を受け取れない場合は完了扱いにせず、同じ候補を次回再送します。接続ID＋メッセージIDで重複しないので、応答喪失時もカードを増やしません。
6. 本人がTimelineの「Gmailからの取り込み待ち」カードで内容を確認し、「取り込む／取り込まない」を選びます。「取り込む」で初めて通常投稿・ルーター・担当・記憶の処理へ進みます。待機中は検索や担当の処理に入りません。メール内の`@agent`や命令は本人の直接依頼には変換しません。
7. 「取り込まない」で待機本文を削除します。「この種を除外」「このFromを除外」は独立したチェックで、破棄時だけ適用します。workerのコードが同じ候補をkakerattaへ再照会し、実際のレシートをBridgeへ戻すと除外ルールが反映されます。Bridgeで行を削除して保存すると解除でき、古いレシートの再送で解除済みルールは復活しません。

`google_review_context`は新規候補を優先し、残りの枠で待機カードの確認を順番に回します。取り込み停止・切断・保存データ削除・期限切れにより現在の候補でなくなったものは配送対象から外れます。本文の保存範囲が変わった場合は最新のcontextを取り直します。受信側は既存カードを勝手に書き換えず、本人の選択結果を返します。

Bridgeの選別済み保存本文は最大200件・設定日数までです。期限は次の参照/同期で適用します。kakerattaへ渡した待機本文は本人の選択まで保持し、Bridgeの削除では回収しません。同期は初回の指定日数取得からGmail履歴差分へ進み、履歴失効時は指定日数の再取得へ戻ります。処理済みIDと受信側のレシートで重複を防ぎます。範囲より古いメールの完全回復は保証しません。

### Hermes cronで定期実行する

担当の回答提出と同じく、取得・モデル結果の検証/提出・配送・確認結果の返却は**Hermes側のPythonコード**で行います。LLMは選別・要約だけを担当し、MCPツールは使いません。cron親モデルも起こさないため、gateのジョブに`--no-agent`を付けます。

1. [check_google_bridge.py](../../tools/google-bridge/hermes/check_google_bridge.py)・[check_calendar_bridge.py](../../tools/google-bridge/hermes/check_calendar_bridge.py)・[google_bridge_worker.py](../../tools/google-bridge/hermes/google_bridge_worker.py)を、自分の`HERMES_HOME/scripts/`（通常`~/.hermes/scripts/`）へコピーし、実行権限（`chmod +x`）を付与します。NemoClaw / OpenShell サンドボックス環境（`/sandbox/.hermes/scripts/`）では、ホスト側から `openshell sandbox upload --name <sandbox> tools/google-bridge/hermes/<script> /sandbox/.hermes/scripts/<script>` で転送します。
2. 先に[Hermes連携](hermes_ja.md)を導入し、既存の`direct_request_worker.py`が同じscriptsにあることを確認します。Google workerはこのruntimeのモデル設定・Hermes CLI起動・利用量/時間制限を再利用します。モデルは親Hermesの設定から選び、選別用に1ターン、1回のworkerで最大5モデルバッチ・5 API呼び出し・250,000トークンとし、モデル処理の時間枠は300秒までに制限します。1バッチは最大10候補です。実行中のモデル呼び出しはトークン上限を超える場合があります。
3. BridgeのMCPキー発行時に表示されたJSONを、同じディレクトリの`google_bridge.json`へ保存し`chmod 600`を適用します。JSONに入れるのは**BridgeのURL・キーだけ**です。同一端末のキーなしモードでは`{"url":"http://127.0.0.1:8094/mcp","key":""}`も使えます。
4. 親Hermesの`config.yaml`には本人の`mcp_servers.kakeratta`を設定します。workerはそのURL・ヘッダーをコードで読みます。選別モデルの隔離homeにはMCP・スキル・外部ツールを追加しません。
5. 本人のHermes環境で次を登録します。古いエージェント付き配送ジョブは二重登録を避けて置き換えます。

```sh
hermes cron create "every 30m" --no-agent --script check_google_bridge.py --deliver local --name google-bridge-mail
```

gateは同期結果の件数だけを見て、判定待ち・未配送候補・本人確認結果の照会があるときにコードworkerを起動します。常に`wakeAgent:false`を返し、配送だけの回と仕事がない回にはLLMを呼びません。workerのサービス別ロックにより重複起動を抑止し、取得・選別の続きを最大10バッチまで処理します。同じ待機カードは1回のworker内で繰り返し照会しません。

workerはモデルの結果を検証してから、`HERMES_HOME/google-bridge/<service>-decisions.json`へmode 0600で保存して提出します。本文原文はこのファイルへ保存せず、生成した要約と判定・ID・tokenだけを保持します。通信失敗時はそのまま再提出して再推論を避け、提出完了、または判定文変更・同意取消など確定的な無効化では削除します。保存先の一時障害などでは保持して再提出します。モデルのセッション履歴は隔離homeに残り得るため、Hermes側の保持方針に従います。配送応答を失った場合も、Bridgeに残る同じ候補をコードで再送して重複を防ぎます。

`<service>-status.json`には成功時の件数、`<service>-worker.log`には固定の失敗案内だけを記録します。原文・要約・キーをcronの標準出力や通知へ出しません。配送先・ツール名・レシートはモデルの出力から組み立てず、設定値・Bridgeの候補・実際のkakeratta応答だけをコードで使います。Bridgeはkakerattaへの接続を持たず、認証済みworkerからの忠実なレシート返却を信頼します。

## 今日・明日のCalendar予定を選んで取り込む

1. Calendarを読み取り権限で接続します。`primary`ではカレンダー一覧権限は不要です。
2. 「04 今日・明日の予定を取り込む」で有効状態を確認し、必要なら「おすすめ設定で有効にする」を押します。詳細設定で対象カレンダーID、タイムゾーン（既定`Asia/Tokyo`）、説明文保存文字数（既定2,000）を指定します。予定取得にGmail側の有効化やBridgeの投稿先設定は不要です。
3. 「今日・明日の予定を今すぐ確認」で、指定TZの今日0時から明後日0時までに重なる予定を取得します。定期予定は各回に展開し、終日は終了日を含まないことを示します。
4. workerが`calendar_triage_context`で取得し、モデルの選別・要約結果を検証して`calendar_triage_submit`でコード提出します。その後`google_review_context`へ`{"service":"calendar"}`を渡し、返されたargumentsを自分のkakeratta MCPの`propose_calendar_import`へ送ります。結果は`google_review_receipt`で戻します。
5. Timelineで本人が「取り込む／取り込まない」を選びます。チェック付き破棄では、単発はカレンダーID＋予定ID、定期予定はカレンダーID＋シリーズIDを除外します。workerのコードが実際のレシートを戻すとBridgeへ反映し、LLMより先に除外します。Bridgeで「この除外を解除」して保存すると今後の新しい回が対象へ戻り、処理済みの回は再提示しません。

Calendarの通常同期は完了した同じ暦日にGoogleで再取得せず、Webの確認ボタンは同日も再取得します。後のGoogle側変更は既存カード・投稿へ自動反映しません。この取得・判定・配送ではGoogle Calendarを書き換えません。キャッシュは終了から24時間または取得から7日を過ぎた後の参照・同期で消去します。Google上の予定と既にkakerattaへ送ったカード・投稿は消しません。

### Calendarの日次ジョブ

メールと同じ個人用JSON・worker・既存の担当worker用runtimeと、本人のkakeratta接続を使います。次はHermes側cronタイムゾーンで朝6時の例です。対象日付はBridgeに設定したIANAタイムゾーンで区切ります。

```sh
hermes cron create "0 6 * * *" --no-agent --script check_calendar_bridge.py --deliver local --name google-bridge-calendar
```

Googleの送信・予定変更ツール、Timelineの本人確認APIはこのworkerから呼びません。Calendarでも配送・レシート確認だけならモデルを使わず、選別が有効な場合だけ隔離セッションを起動します。Gmail・Calendarともに、実Googleアカウントと実Hermesを使った連携をプロジェクト所有者が検証済みです。

## 送信・予定変更（直接依頼で実行）

接続設定の「許可する操作の範囲」で、使う権限を選んでGoogleへ接続します。初期値ではすべて有効です。Gmail送信は`gmail.send`（Sensitive）、下書き保存は`gmail.compose`（Restricted）、Calendar変更は`calendar.events`と所有権確認用の`calendar.calendarlist.readonly`を使います。composeはGoogle側では送信も許可します。[Gmail公式scope一覧](https://developers.google.com/workspace/gmail/api/auth/scopes)を確認してください。

1. 本人がHermesに「〇〇さんにメールを送って」「明日10時に予定を入れて」のように直接頼みます。
2. Hermesが`google_action_run`を呼ぶと、確認画面を挟まずにその場で実行します。`userRequest`には本人の依頼文をそのまま入れます。この依頼文は監査ログに残ります。`idempotencyKey`は16〜128文字で操作ごとに生成し、同じ操作の再試行だけに再利用します。再試行しても二重送信せず、保存済みの結果を返します。
3. 実行した内容と依頼文は管理画面の「03 実行履歴」で確認できます。

このツールは本人の直接依頼があったときだけ使うよう、ツール説明とMCPの指示で制限しています。メール・予定・他者のTimeline投稿の内容、cronや判定ジョブ、Hermes自身の判断では実行しません。ただしこの制限はHermesとモデルが指示に従うことを前提にしており、サーバー側で依頼の真正性は検証できません。判定用cronジョブでは、Hermes設定の`tools.exclude`などで`google_action_run`を外すことをおすすめします。

メールの例:

```json
{"type":"gmail_send","idempotencyKey":"mail-operation-example-001","mail":{"to":["recipient@example.com"],"cc":[],"subject":"確認","body":"送信前にこの本文を画面で確認してください。"}}
```

返信は`mail.replyToMessageId`に元メッセージIDを指定し、元件名そのまま、または`Re: `を付けた件名を使います。アプリが元メールのMessage-ID・スレッドを取得して固定します。宛先は必ず明示してください。`gmail_draft`はGoogleへ下書きを保存する別操作です。保存済み下書きIDからの送信は提供せず、送信時は本文を固定した新たな操作案を承認します。送信元は接続アカウントのみ、To+CCは最大20件、本文はUTF-8で16,000バイトまで、添付・BCC・HTML送信は非対応です。

予定作成の例:

```json
{"type":"calendar_create","idempotencyKey":"calendar-operation-example-001","calendarId":"primary","sendUpdates":"all","createMeet":false,"event":{"summary":"相談","description":"内容を確認","location":"オンライン","start":{"dateTime":"2026-10-20T10:00:00+09:00","timeZone":"Asia/Tokyo"},"end":{"dateTime":"2026-10-20T11:00:00+09:00","timeZone":"Asia/Tokyo"},"attendees":[{"email":"guest@example.com"}]}}
```

`calendar_update`は`eventId`も指定し、対応する全項目（summary、description、location、start、end、attendees）を置き換えます。省略値は空として扱うため、残す値も含めてください。他の項目・既存会議リンクは維持します。`calendar_delete`はeventを付けず対象IDを指定します。所有者がownerのカレンダーだけを対象とし、繰り返し全体・特殊な予定種別・代理管理は拒否します。繰り返し予定の個別インスタンスは変更・削除できます。新規繰り返し予定の作成は未対応です。

時刻指定にはRFC3339のオフセットと一致するIANA timezoneを両端へ指定します。終日は`{"date":"2026-10-20"}`のように指定し、終了日は含みません。参加者は最大20件です。`sendUpdates`は`none` / `all` / `externalOnly`から明示指定します。`none`でもGoogleの仕様上、一部の通知が届く可能性があります。`createMeet:true`は新規予定でのみ利用でき、Google側でMeet作成が許されるカレンダーが必要です。[予定作成の公式仕様](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert)を確認してください。

更新・削除は操作案作成時のETagを再照合し、書き込みにもIf-Matchを付けます。予定が変わった場合は新たに確認・承認してください。[Googleの条件付き変更](https://developers.google.com/workspace/calendar/api/guides/version-resources)に従います。再接続や切断でも未実行の操作案は無効になります。

通信失敗やタイムアウトで結果が判明しない場合は`unknown`となり、自動再送しません。**Google上の送信済み・下書き・予定を本人が確認してから**、必要な場合だけ新しい操作を依頼してください。配信の完全なexactly-onceは保証しません。実行前に承認を消費して保存することで、アプリ側の再試行による二重送信を防ぎます。

同時に保持する未完了操作案はサービスごとに最大8件です。操作案は暗号化保存し、完了・却下・切断時、または期限切れ後の次回ジャーナル参照時に本文・予定データを消去します。期限到達だけで起動する独自常駐ループは設けていません。操作者・接続ID・操作種別・ハッシュ・結果・承認/実行日時だけの監査情報と冪等キーのハッシュは再実行防止のため保持します。Googleデータを削除するにはサービスを切断してください。監査情報を含む全アプリデータは停止後に専用dataDirを消去することで削除できますが、冪等性履歴も消えるため旧キーの操作案を再利用しないでください。バックアップとGoogle上のメール・予定は別途本人が管理します。

## 再認証・切断・復旧

- 再認証や2つ目のサービス接続では3つのclient設定欄をすべて空欄にすると保存済みclientを使います。モデル転送同意は再び確認します。
- 「接続テスト」はGmailの最大1件検索、Calendarの今後24時間の最大1件検索です。画面へ本文は返しません。
- 通常切断は選んだサービスの資格情報と未完了OAuthを削除し、そのサービスの未完了操作案の本文・予定データを消去して無効化します。他方の接続を維持します。
- 「Google側の許可も失効」を選ぶとrevokeを試みます。これは**同じプロジェクトの全クライアント**の許可に影響するため、他方や別ツールも再認証が必要になります。失効要求に失敗してもローカル資格情報は削除します。厳密な独立失効にはサービス別プロジェクトを使ってください。
- `redirect_uri_mismatch`は登録URIを画面の値と完全一致させて解消します。
- 再認証要求はTesting期限、ユーザー取消、Gmailのパスワード変更等を確認して再接続します。
- Calendar一覧だけのscope不足は一覧権限を追加します。API未有効・組織のポリシー等でも拒否される場合があります。
- OAuthは同じタブで10分以内に完了します。コードは単回使用です。失敗したら接続を開始し直してください。
- 管理キーを紛失したらプロセスを停止して、保護された設定ファイルのadminTokenを新しいランダム値へ変更します。管理セッションは管理キーの指紋にも束縛されるため、キー変更後の再起動で既存ブラウザーセッションも無効になります。MCPキーは独立しているため、必要なら画面から失効させます。
- リポジトリの更新後にworker実行時 `Unknown tool (google_review_context)` などのエラーが発生する場合は、ホスト側で稼働中の `google-bridge` バイナリが古い可能性があります。`mise exec -- go build -o ~/.local/bin/google-bridge ./tools/google-bridge/cmd/google-bridge` で再ビルドし、`systemctl --user restart google-bridge.service` で再起動してください。あわせてサンドボックス内のスクリプト群も最新のものを再アップロードしてください。

資格情報とrefresh・切断・同期・転送は同じWorkspaceロックで直列化します。既にHermesへ渡ったデータを回収することや、Google Cloudプロジェクトの削除は行いません。

鍵rotationでは旧鍵をkeys mapへ残し、現行keyVersionを切り替えて再起動します。各接続の接続テストと「操作案を更新」で接続・操作ジャーナルの暗号文を書き換え、メール・予定の保存一覧を更新してから旧鍵を退役させます。データのバックアップに設定ファイルを同梱しないでください。バックアップされた旧暗号文の保持・消去は本人のバックアップ運用で管理します。

## 検証範囲

プロジェクト所有者が、Google OAuthとHermes接続を含むGmail・Calendarの一連の実環境連携を検証済みです。hackathonギャラリーも、その検証時のキャプチャで更新済みです。この確認はGmail・Calendarの取り込みを対象とし、送信・下書き・予定変更の全操作についての実環境検証を示すものではありません。

fake Google HTTPでOAuth、scope、インスタンス分離、暗号化、refresh競合、失効、切断、MCPに加え、本人承認境界、改変・期限切れの拒否、並列実行、未知の結果の再送停止、予定ETag、返信MIME、Calendar作成・更新・削除に加え、差分同期・履歴失効・中断再開・保持期限・Hermes役の中継によるTimelineレビュー受付・破棄・再提示防止・検索隔離、Bridgeからの直接配送がないこと、旧投稿先資格情報の起動時削除を検証しています。実ブラウザーでログイン・キー発行・MCP初期化・キー失効・同意必須・モバイル表示・ログアウトを確認しています。自動テストは実Googleアカウントを必要とせず、変更もしません。自分の設定と同意後、Gmail検索/詳細とCalendar期間検索を各1回確認してください。送信・予定変更テストは含めません。
