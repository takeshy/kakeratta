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
Firebase認証は共有の `KAKERATTA_TOKEN` より優先されます。トークン方式は、手元で
1人で動かすときのための方式です。

## デプロイ

必要なもの: 認証済みの `gcloud` CLI、Terraform 1.7以降、Node.js、`jq`、課金を有効に
したGCPプロジェクト。実行アカウントには、API、Cloud Run、Firestore、Cloud Storage、
Firebase Hosting、Cloud DNS、IAM、Artifact Registry、Secret Managerを管理する権限が
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

Firebase認証を設定しない場合は、共有のアクセストークンに切り替わります。その方式での
初回ログイン用URLは次で取得します。

```sh
cd infra
terraform output -raw login_url
```

このURLは公開しないでください。Terraform stateにも生成したトークンが含まれるため、
同じく安全に保管してください。

データのある場所には削除の保護を掛けています。キャプチャのバケットは `prevent_destroy`、
Firestoreは削除保護、Cloud Runサービスは `deletion_protection` です。applyを通すためでは
なく、消すと決めたときにだけ外してください。

バックエンドのログは次で読めます。

```sh
gcloud run services logs read kakeratta --region=asia-east1 --project=kakeratta
```

## VM構成の残り

以前の単一VM構成（Compute Engine上のCaddyが `sslip.io` 名でTLSを終端する形）は
無くなりました。ただし `google_compute_address.app` と `google_compute_firewall.web`
の2つは宣言が残っており、`fallback_url` 出力もそのアドレスから作られているため、
現在はどこにも繋がりません。Cloud Run経路の確認が済んだら削除してください。
