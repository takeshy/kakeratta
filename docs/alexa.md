# Alexaスキルの接続

日本語と英語のカスタムスキルとして、声でのかけら投稿、担当の返事、予定の読み上げに対応します。コードの配置だけではAlexaから利用できません。Amazon側でスキルを作成し、以下の設定を行ってください。

## 対応言語・地域

同じスキル・HTTPSエンドポイントで以下の6ロケールに対応します。Consoleで各言語を追加し、対応するJSONを読み込んでビルドしてください。

| 言語・地域 | 発話定義 | 呼び出し名 |
| --- | --- | --- |
| 日本語・日本 | [ja-JP.json](../integrations/alexa/ja-JP.json) | かけらった |
| 英語・米国 | [en-US.json](../integrations/alexa/en-US.json) | kakeratta notes |
| 英語・英国 | [en-GB.json](../integrations/alexa/en-GB.json) | kakeratta notes |
| 英語・カナダ | [en-CA.json](../integrations/alexa/en-CA.json) | kakeratta notes |
| 英語・豪州 | [en-AU.json](../integrations/alexa/en-AU.json) | kakeratta notes |
| 英語・インド | [en-IN.json](../integrations/alexa/en-IN.json) | kakeratta notes |

英語版は2語の呼び出し名を使用します。各地域での認識精度とモデルビルドはConsole・実機で確認してください。公開時はDistributionの対象地域と各ロケールのスキル説明も設定します。端末の言語とアカウントのマーケットプレイスに対応する言語版が必要で、全地域に配信する設定だけでは言語対応になりません。[Amazonの多言語対応仕様](https://developer.amazon.com/en-US/docs/alexa/custom-skills/develop-skills-in-multiple-languages.html)

## 話しかけ方

- 「アレクサ、かけらったを開いて」
- 「メモして、明日は散歩に行きたい」
- 「返事を聞いて」
- 「予定を教えて」
- 「ストップ」

英語では「Alexa, open Kakeratta Notes」で起動後、次のように話します。

- “Take a note, I want to go for a walk tomorrow.”
- “Read my replies.”
- “What's on my schedule?”
- “Stop.”

案内・エラー・再質問はAlexaリクエストのロケールに合わせます。英語の投稿には `language: en` を保存し、既存の担当処理が投稿の言語で返事を生成します。保存済みの返事・担当名・予定本文をその場で翻訳する機能はありません。連携画面と連携設定は、アプリの表示言語またはブラウザーの言語に応じて日英を切り替えます。

「メモして」だけでも内容を聞き返します。認識した全文を読み上げた後、「はい」で初めて投稿します。「いいえ」または「言い直して」で入力し直せます。「返信して」も同じ確認手順で、最後に読み上げた担当の返事への返信として保存します。確認前に「ストップ」「キャンセル」で終了すると投稿しません。

起動時・聞き取れない時の案内は短くし、長い説明は「ヘルプ」の時にだけ読み上げます。端末の聞き取り終了タイミングそのものはAlexa側の制御です。

投稿は通常のタイムラインに入り、担当が非同期で返事を作ります。受け付け直後にはまだ返事がない場合があります。同じ音声セッションでは直前に投げたかけらの返事、新しいセッションでは最近100件の会話に含まれる担当の返事を、最新の1件だけを読み上げます。その返事に「返信して」で返信できます。1件700文字までで、続きやHTMLの図解はアプリで確認します。「最新の未読」の管理や、返事が届いたときの自動読み上げはありません。

予定はアプリに保存された今後の予定を最大5件、アカウントのタイムゾーンで読み上げます。未設定・不正・サーバー依存の `Local` タイムゾーンは、日本語では日本時間、英語ではUTCを使い、使用するゾーンを音声で伝えます。言語や地域から現在地のタイムゾーンを推測しません。米国英語は月・日、他の英語ロケールは日・月の順に、年とAM/PM付き時刻を読み上げます。外部カレンダーを直接検索する機能ではありません。

## サーバー設定

FirebaseログインのあるWeb/combinedサーバーをHTTPSで公開し、次の環境変数を設定します。ローカルの認証なしモードでは有効化できません。

| 環境変数 | 値 |
| --- | --- |
| `KAKERATTA_ALEXA_SKILL_ID` | Consoleが発行する `amzn1.ask.skill.…` |
| `KAKERATTA_ALEXA_CLIENT_ID` | この連携専用のOAuthクライアントID。例: `kakeratta-alexa` |
| `KAKERATTA_ALEXA_CLIENT_SECRET` | 生成した十分に長いランダムな秘密値。サーバーのSecret設定とConsoleに同じ値を設定 |
| `KAKERATTA_ALEXA_REDIRECT_URLS` | Consoleに表示されるRedirect URLsを空白区切りで列挙。クエリ文字列も含めた完全一致で検証 |

既存のFirebase認証設定と `KAKERATTA_PUBLIC_URL` も設定してください。秘密値をソース管理やフロントエンドに入れないでください。Terraform/VMの既存環境定義にはAlexa固有の値を追加していないため、実際の配備経路の環境変数・Secret注入にも上記を追加する必要があります。

## Amazon Developer Console

1. Custom Skillを作成し、日本語と対象の英語ロケールを追加します。HTTPSの独自エンドポイントを使います。
2. 各ロケールのInteraction ModelのJSON Editorに、上の表の対応するJSONを読み込み、保存してビルドします。Consoleでの発話認識・呼び出し名の検証も行ってください。
3. Endpointに `https://<公開ホスト>/api/alexa` を指定します。地域別エンドポイントを設定する場合も同じURLを使えます。Amazonが信頼するCAのTLS証明書を使用します。
4. Account Linkingを有効にし、Authorization Code Grantを選びます。
   - Authorization URI: `https://<公開ホスト>/alexa`
   - Access Token URI: `https://<公開ホスト>/api/alexa/token`
   - Client ID / Client Secret: 上記環境変数と同じ値
   - Client Authentication Scheme: HTTP Basic（リクエスト本文の資格情報も対応）
   - Scope: `alexa`
   - アカウント連携なしでの有効化を許可しない設定
5. Consoleに表示された各地域のRedirect URLsをすべてサーバーに設定します。OAuthクライアントID・シークレットは共通です。
6. TestをDevelopmentにし、同じAmazonアカウントのAlexaアプリで連携を開始します。かけらったにGoogleログイン後、「Alexaと連携する」を押します。

連携ページは既存のFirebaseログインを使い、ポップアップではなくリダイレクトでGoogle認証します。公開ホストをFirebaseの認証許可ドメインに設定してください。本番では `authDomain` を `kakeratta.net` にし、Nginxで `/__/auth/` を `https://kakeratta.firebaseapp.com` に透過転送します。Google OAuthのウェブクライアントにも `https://kakeratta.net/__/auth/handler` を承認済みリダイレクトURIとして追加します（既存URIは残します）。別ドメインの `authDomain` のままだと、サードパーティーストレージを制限するブラウザーでログイン画面に戻ることがあります。Alexaアプリ内ブラウザーと端末で、この往復が成功することを確認してください。

公開する場合は別途スキルの公開情報、プライバシーポリシー、Amazonの審査が必要です。規約ページを配備後、Privacy Policy URLには `https://kakeratta.net/privacy/`、Terms of Use URLには `https://kakeratta.net/terms/` を設定できます。このリポジトリの変更はAmazon上の登録・公開・本番配備を行いません。

## 連携解除と確認

かけらったの「設定 → 外部連携 → Alexa連携を解除」でアクセス・リフレッシュトークンと保留中の認可コードを無効化できます。Alexaアプリ側でのスキル無効化と併せて使えます。同じワークスペースには1つのAlexa連携を保持し、新たな連携が完了すると以前の資格情報が無効になります。アクセスは1時間、リフレッシュは連携から365日で期限切れになり、その後は再連携が必要です。Judgeの一時アカウントは連携できません。

配備後は、未連携時の案内、投稿がアプリに1件だけ現れること、担当の返事、アカウントの時刻での予定読み上げ、停止、連携解除後にデータを読めないことを実機で確認してください。会話内容やトークンをリクエストログに記録しない運用にしてください。

## 仕様とローカル検証

- [Amazon: HTTPSスキルと署名・時刻検証](https://developer.amazon.com/en-US/docs/alexa/custom-skills/host-a-custom-skill-as-a-web-service.html)
- [Amazon: Account Linking Concepts](https://developer.amazon.com/docs/alexaplus/account-linking/account-linking-concepts.html)
- [Amazon: 日本語のSearchQueryスロット](https://developer.amazon.com/ja-JP/docs/alexa/custom-skills/slot-type-reference.html)
- [Amazon: アカウント連携のトラブルシューティング](https://www.developer.amazon.com/docs/alexaplus/account-linking/troubleshooting-account-linking.html)

```sh
mise exec -- go test ./internal/alexa ./internal/server
mise exec -- node --test tests/*.test.js
mise exec -- npm --prefix frontend run build
```

テストはローカルの署名用証明書とストアを使います。Amazonの認証情報は不要ですが、Consoleでのモデルビルド、Firebase/Googleとの連携往復、Alexa実機の確認を代替するものではありません。

## Googleアカウントを使わない審査

アカウントリンク画面の「Review access / 審査用ログイン」で、事前作成した審査用メールアドレス・パスワードを使用できます。OAuthの情報は保持され、同じブラウザー内で同意画面に進みます。[審査アカウントの作成・手順](judge-access.md#alexa-certification)を参照してください。

## 対話モデルの更新

投稿確認・言い直し・返信には、新しいJSONを対象ロケールのJSON Editorへ貼り付け、Save Model / Build Modelを実行してください。サーバーの更新だけではYes/No・返信インテントとスロット聞き取り用ダイアログは追加されません。
