# Google Cloudへのデプロイ

[English](README.md)

## 現在の本番：共有VMでの暫定運用

公開環境ではCloud Run構成を推奨します。10/31の締め切り後、審査まで約1か月間、本番を変更せず
維持する費用を抑えるため、2026-10-06に共有Compute Engine VMへ移しました。
Webとworkerは別コンテナのまま、Firestore・GCS・Firebase認証・永続キューを使います。

HTTPSロードバランサーとNginxが静的な画面を配信し、API/SSEと認証付き内部pushを
転送します。GemiHub・Geo MCPと同居するVMは固定容量で、自動フェイルオーバーはなく、
workerは常時稼働します。

構築・実行アカウント・デプロイ・ロールバックは[共有VMの運用手順](shared-vm/README.md)
（英語）を参照してください。現在の `cloudbuild.yaml` はイメージと画面をビルドし、
画面のアーカイブをアップロードして、IAP/OS Login経由で両コンテナを更新します。
本番ログは `geminihub-486523` にあります。

```sh
gcloud logging read 'resource.type="gce_instance" AND labels.service_name:"kakeratta"' --project=geminihub-486523 --freshness=1d --limit=100 --format=json
```

以下は保持しているFirebase Hosting・Cloud Run構成と `infra/deploy.sh` の説明です。
現在の本番リリース手順とは異なり、Firebase Hostingの更新ではVMの画面は更新されません。
元のTerraformモジュールは `shared_vm_ip` が設定されていれば移行済みのDNSと
Pub/Sub・Schedulerのエンドポイントを維持します。本番設定やタイマーキューの切り替えは
共有VMの運用手順を確認してから行います。

## Terraformのstateとtfvars

アプリ用と共有VM用のstateは、`takeshy-work` プロジェクトの非公開バケット
`takeshy-work-kakeratta-terraform` で管理します。保存先はそれぞれ `state/app` と
`state/shared-vm`、tfvarsは `vars/app` と `vars/shared-vm` です。バケットは別途作成し、
均一アクセス・公開禁止・世代管理を有効にしています。アプリとVMのプロジェクトから
分けることで、ビルドアカウントのStorage権限を継承しないようにしています。

TerraformをPATHに用意して、次のラッパーを使います。miseなら各コマンドの前に
`mise exec terraform@1.13.4 --` を付けられます。

```sh
python3 infra/terraform.py shared-vm init
python3 infra/terraform.py shared-vm plan -out=/tmp/shared-vm.tfplan
python3 infra/terraform.py shared-vm apply /tmp/shared-vm.tfplan
```

元のアプリ基盤には `shared-vm` の代わりに `app` を指定します。planと保存済みplanを
使わないapplyでは、GCSのtfvarsを非公開の一時ファイルに取得し、終了時に削除します。
保存済みplanのapplyには、そのplanに含まれる変数を使います。認証は既存の認証用環境変数、
またはログイン済みgcloudの短期トークンを使い、backend設定にトークンは保存しません。
編集用のtfvarsは `python3 infra/terraform.py app fetch-vars`（または `shared-vm`）で
取得します。既存のローカルファイルを意図して置き換える場合だけ `--force` を付けます。
ローカルの無視対象tfvarsを編集した後は `python3 infra/terraform.py app upload-vars`
または `... shared-vm upload-vars` でGCSへ保存します。state・tfvarsの内容は表示・コミット
しません。古いローカルstateは復旧用であり、現在の保存先はGCSです。
[移行と復旧の説明](../docs/system/operations.md#terraform-state-and-variables)も参照してください。

## Cloud Run構成

公開時のkakerattaは、画面・Web API・workerを分けて動きます。画面は **Firebase Hosting** が配信し
（`frontend/dist`。バイナリは画面を持ちません）、`/api/**` だけを
**Cloud Run** へrewriteします。Cloud Run側ではGoのバイナリが動き、タイムライン・
設定・メモはFirestore、キャプチャは非公開のCloud Storageバケットに入ります。
インスタンスのディスクには何も残さないので、画面は状態を持たないサービスの前に
置いた静的サイトであり、画面だけの変更はイメージを作り直さずに反映できます。

## マルチユーザー認証

本番のジョブ処理は、Web APIが保存した要求をPub/Subが別のCloud Run workerへ配信します。
workerのCPU・メモリ・台数はTerraformで管理し、利用に合わせて調整します。仕事がないときはゼロ台にできます。
`queue_worker_enabled` は標準で `true`。必要に応じて `worker_max_instances` を増やせます。
未送信要求はCloud Schedulerが毎分再送します。現在のユーザー1名の構成では専用の移行処理は
設けず、切り替え前に既存処理を完了させます。詳細は
[運用手順](../docs/system/operations.md#push-worker-operations)を参照してください。
`queue_worker_enabled = false` にすると従来のcombined方式になります。

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
Firebase Hosting、Cloud DNS、Cloud Tasks、Pub/Sub、Cloud Scheduler、IAM、Artifact Registry、Secret Managerを管理する権限が
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

現在の `kakeratta-main-deploy` トリガーは共有VMを更新します。移行中は
`cloudbuild.yaml` と同じ内容のインライン設定を使うため、リポジトリのビルド定義を
変えるときはトリガーも更新するか、公開済みのファイルを参照する設定へ切り替えます。
[共有VMのデプロイ手順](shared-vm/README.md#deployment-and-recovery)を参照してください。

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

デプロイ先プロジェクトのSecret Managerに `kakeratta-wheatherapi-key` を作成し、APIキーを有効なバージョンとして登録します。この綴りが登録済みの名前です。保持しているCloud Run用Terraform構成は `latest` をCloud Runの環境変数 `KAKERATTA_WEATHERAPI_KEY` に紐付けます。実行用サービスアカウントには既存のSecret Manager読み取り権限があります。キーの値はソースコード・Dockerイメージ・ビルドの置換変数・Terraform stateには入りません。Terraformは登録済みシークレットへの参照だけを管理します。

別のシークレットや固定バージョンを使う場合は、Cloud Run構成ではTerraformの `weatherapi_secret_id` / `weatherapi_secret_version` を設定します。VMでは実行マニフェストのシークレット参照を更新します。新しいCloud Runインスタンスが起動時に読み込み、既存インスタンスは読み込み済みの値を保持します。

## Bluesky連携

TerraformはOAuth署名と接続情報の暗号化に使う秘密鍵を生成し、Secret Managerから `KAKERATTA_BLUESKY_KEY` としてCloud Runへ渡します。鍵はTerraform stateにも含まれるため非公開で管理し、既存の鍵を維持してください。`KAKERATTA_PUBLIC_URL` は既定で `https://<domain>` です。別の公開オリジンを使う場合は `bluesky_public_url` を設定します。

サーバー・Terraform・画面の更新が必要です。利用者ごとの開発者登録やAPIキーは不要で、各ユーザーがBluesky側で認可します。メタデータの公開経路、同じタブでの認可、鍵の管理、途中失敗の復旧は[運営側の設定](../docs/social-sharing_ja.md)を参照してください。
