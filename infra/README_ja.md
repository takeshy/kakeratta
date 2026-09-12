# Google Cloudへのデプロイ

[English](README.md)

このTerraform構成は、kakerattaをCompute Engineの単一VMで動かします。
タイムライン、画像、設定、ペルソナの記憶は専用の永続ディスクへ保存します。
コンテナはArtifact Registry、アクセストークンはSecret Managerに置き、Caddyが
固定IPの`sslip.io`名に対するHTTPS証明書を取得します。

## デプロイ

認証済みの`gcloud` CLI、Terraform 1.7以降、課金を有効にしたGCPプロジェクトが必要です。
実行アカウントには、API、Compute Engine、IAM、Artifact Registry、Secret Managerを
管理する権限が必要です。

```sh
gcloud auth application-default login
./infra/deploy.sh
```

既定ではプロジェクト`kakeratta`の`asia-northeast1-b`へ構築します。
`KAKERATTA_PROJECT_ID`、`KAKERATTA_REGION`、`KAKERATTA_ZONE`、またはTerraform変数で
変更できます。スクリプトはバージョン付きイメージのビルド、Terraformの適用、VM再起動、
HTTPS応答の確認まで行います。

## 独自ドメイン

Terraformは`kakeratta.net`のCloud DNSゾーンと、固定IPを指すAレコードを作成します。
お名前.comのネームサーバー設定には、次で表示される4つの値を登録してください。

```sh
cd infra
terraform output name_servers
```

委任が反映されると、Caddyが`kakeratta.net`と`www.kakeratta.net`のHTTPS証明書を自動取得します。

初回ログイン用URLは次のコマンドで取得します。このURLは公開しないでください。

```sh
cd infra
terraform output -raw login_url
```

Terraform stateには生成したアクセストークンが含まれるため、安全に保管してください。
データディスクには`prevent_destroy`を設定しています。ディスクと保存データを削除するときだけ、
このライフサイクル設定を意図的に外してください。

VMの起動状況は次のコマンドで確認できます。

```sh
gcloud compute ssh kakeratta --zone=asia-northeast1-b \
  --command='sudo journalctl -u google-startup-scripts.service; sudo docker ps'
```
