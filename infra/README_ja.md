# Google Cloudへのデプロイ

[English](README.md)

公開時のkakerattaは、独立した2つで動きます。画面は **Firebase Hosting** が配信し
（`frontend/dist`。バイナリは画面を持ちません）、`/api/**` だけを
**Cloud Run** へrewriteします。Cloud Run側ではGoのバイナリが動き、タイムライン・
設定・メモはFirestore、キャプチャは非公開のCloud Storageバケットに入ります。
インスタンスのディスクには何も残さないので、画面は状態を持たないサービスの前に
置いた静的サイトであり、画面だけの変更はイメージを作り直さずに反映できます。

## マルチユーザー認証

同じGoogle Cloudプロジェクトに Firebase Web アプリを作り、**Firebase Authentication >
Sign-in method** でGoogleを有効にします。デプロイスクリプトが有効なWebアプリの設定を
Firebase Management APIから直接読むため、ローカルの設定ファイルやAPIキーの環境変数は
不要です。

セッションCookieの名前は `__session` です。Firebase Hosting は Cloud Run へ渡す前にそれ以外の
Cookieをすべて剥がすため、別名だとログイン直後から `login required` になります。
ログインした利用者ごとに、Firebaseの `uid` で分離された保存先が割り当てられます。
公開環境ではFirebase認証が必須です。

## デプロイ

必要なもの: 認証済みの `gcloud` CLI、Terraform 1.7以降、Node.js、`jq`、課金を有効に
したGCPプロジェクト。実行アカウントには、API、Cloud Run、Firestore、Cloud Storage、
Firebase Hosting、Cloud DNS、Cloud Tasks、IAM、Artifact Registry、Secret Managerを管理する権限が
必要です。

```sh
gcloud auth application-default login
./infra/deploy.sh
```

既定ではプロジェクト `kakeratta` を使い、ビルドは `asia-northeast1`、実行は
`asia-east1` です（Cloud RunはFirebase Hostingがrewriteできるリージョンに置く必要が
あります）。`KAKERATTA_PROJECT_ID`、`KAKERATTA_REGION`、`KAKERATTA_RUN_REGION`、
またはTerraform変数で変更できます。

スクリプトは、バージョン付きイメージのビルド、Terraformの適用、`node frontend/build.mjs`、
Firebase Hostingへのデプロイ、`https://PROJECT_ID.web.app` のHTTPS応答確認まで行います。

初回のインフラ構築後は、Cloud Build の `kakeratta-main-deploy` トリガーが `main` への
pushを検知し、既存のCloud RunサービスとFirebase Hostingを更新します。この自動処理は
Terraformを適用しないため、実行環境の設定や秘密情報は維持されます。GitHub接続にはOAuth
によるインストールが必要なため、リポジトリ接続とトリガーはCloud Buildで一度だけ作成します。

### リマインダーのWeb Push通知

Terraformで生成済みのVAPID公開鍵と `mailto:takeshy.work@gmail.com` を設定し、
既存のSecret Managerシークレット `kakeratta-vapid-private:latest` を
`KAKERATTA_PUSH_PRIVATE_KEY` として参照します。シークレットとその値は運営者が登録し、
Terraformは秘密鍵の内容をstateへ読み込みません。実行サービスアカウントには既存の
Secret Manager参照権限があります。別の環境では `push_public_key`、`push_subject`、
`push_private_key_secret`、`push_private_key_version` を変更してください。

Cloud Buildによるイメージ更新の前に、現在のイメージタグを指定してTerraformの変更を
適用してください。mainへのpushによるデプロイでは、サービスの環境設定を維持します。
鍵はデプロイをまたいで保持し、交換するときは公開鍵と秘密鍵を同時に更新してください。
利用者は設定の「この端末」で通知を有効にします。鍵を交換した場合は無効化してから
再び有効化します。[運用設定](../docs/system/operations.md#web-push-configuration)も参照してください。

### 道具：MCPサーバー

MCPサーバーの設定は利用者ごとではなくデプロイごとです。`infra/terraform.tfvars`（コミットしない）に書くか、
`deploy.sh` に環境変数 `KAKERATTA_MCP_SERVERS` で渡します。

```hcl
mcp_servers = [
  { name = "geo", url = "https://geo.example.com/mcp", headers = { Authorization = "Bearer …" } },
]
```

隣にある geo-home-mcp に繋ぐ場合は `./infra/link-geo-home.sh` を実行すると、geo-home-mcp のTerraform output
（`mcp_url` と bearer トークンの `mcp_api_key`）からこのブロックを書き出すので、キーを手で写す必要はありません。
チェックアウトが別の場所なら `GEO_HOME_DIR` で指定します。

Cloud Runには `KAKERATTA_MCP_SERVERS` として渡ります。ヘッダーにトークンを含み得るため sensitive ですが、
Terraform stateには残るので、stateは安全に保管してください。
Web検索に使うTavilyの鍵はここにはなく、利用者が自分の設定画面で入れます。

画面だけの変更にイメージは要りません。

```sh
node frontend/build.mjs
npx firebase-tools deploy --only hosting --project kakeratta
```

## 独自ドメイン

Terraformは `kakeratta.net` のCloud DNSゾーンと、Firebase Hostingに必要な3つの
レコードを作ります。apexの `A` レコード（Hostingのアドレス）、所有を示す
`hosting-site` の `TXT` レコード、`www` の `CNAME`（`kakeratta.web.app`）です。
次で表示される4つの値を、レジストラのネームサーバー設定に登録してください。

```sh
cd infra
terraform output name_servers
```

委任が反映されたら、Firebaseコンソールの **Hosting > カスタムドメイン** で
`kakeratta.net` を追加します。証明書の取得と更新はHostingが行います。DNSの切り替え中も
`https://kakeratta.web.app` は使えるので、確認はこちらで行ってください。

## デプロイしたあと

アプリのURLを開き、Googleでログインしてください。

バックアップ（設定→バックアップ）は同じバケットの `exports/` 以下に書き出し、
1時間だけ有効な署名URLをブラウザに渡します。ダウンロード自体はCloud Runを通りません。
`exports/` 以下はライフサイクルルールで1日後に削除され、署名には実行用サービス
アカウントが自分自身に対して `roles/iam.serviceAccountTokenCreator` を持つことが必要です。

データのある場所には削除の保護を掛けています。キャプチャのバケットは `prevent_destroy`、
Firestoreは削除保護、Cloud Runサービスは `deletion_protection` です。applyを通すためでは
なく、消すと決めたときにだけ外してください。

バックエンドのログは次で読めます。

```sh
gcloud run services logs read kakeratta --region=asia-east1 --project=kakeratta
```

## Firestoreのインデックス

APIの更新前に `firestore.indexes.json` のインデックスを公開し、作成が完了するまで待ってください。Firestoreから全件を読み出さずに親投稿を20件ずつ取得するためと、意味検索のベクトルインデックスのために必要です。

```sh
npx firebase-tools deploy --only firestore:indexes --project kakeratta
```

## WeatherAPI

デプロイ先プロジェクトのSecret Managerに `kakeratta-wheatherapi-key` を作成し、APIキーを有効なバージョンとして登録します。この綴りが登録済みの名前です。TerraformとCloud Buildは `latest` をCloud Runの環境変数 `KAKERATTA_WEATHERAPI_KEY` に紐付けます。実行用サービスアカウントには既存のSecret Manager読み取り権限があります。キーの値はソースコード・Dockerイメージ・ビルドの置換変数・Terraform stateには入りません。Terraformは登録済みシークレットへの参照だけを管理します。

別のシークレットや固定バージョンを使う場合は、Terraformの `weatherapi_secret_id` / `weatherapi_secret_version` とCloud Buildの `_WEATHERAPI_SECRET` / `_WEATHERAPI_SECRET_VERSION` を揃えてください。新しいCloud Runインスタンスが起動時に読み込み、既存インスタンスは読み込み済みの値を保持します。

## Bluesky連携

TerraformはOAuth署名と接続情報の暗号化に使う秘密鍵を生成し、Secret Managerから `KAKERATTA_BLUESKY_KEY` としてCloud Runへ渡します。鍵はTerraform stateにも含まれるため非公開で管理し、既存の鍵を維持してください。`KAKERATTA_PUBLIC_URL` は既定で `https://<domain>` です。別の公開オリジンを使う場合は `bluesky_public_url` を設定します。

サーバー・Terraform・画面の更新が必要です。利用者ごとの開発者登録やAPIキーは不要で、各ユーザーがBluesky側で認可します。メタデータの公開経路、同じタブでの認可、鍵の管理、途中失敗の復旧は[運営側の設定](../docs/social-sharing_ja.md)を参照してください。
