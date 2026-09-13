# Google Cloud deployment

## Multi-user authentication

Create a Firebase Web app in the same Google Cloud project and enable Google
under **Firebase Authentication > Sign-in method**. The deploy script reads the
active Web app configuration directly from the Firebase Management API; no
local Firebase config file or API-key environment variable is needed.

[日本語](README_ja.md)

The production path runs kakeratta on Cloud Run, stores structured user data in
Firestore and captures in a private Cloud Storage bucket, and serves the web UI
through Firebase Hosting. The previous Compute Engine resources remain during
the cutover and can be removed after the Cloud Run deployment is verified.

## Deploy

Requirements: an authenticated `gcloud` CLI, Terraform 1.7 or later, and a GCP
project with billing enabled. The current account needs permission to manage
project services, Compute Engine, IAM, Artifact Registry, and Secret Manager.

```sh
gcloud auth application-default login
./infra/deploy.sh
```

The defaults deploy project `kakeratta` to `asia-northeast1-b`. Override them
with `KAKERATTA_PROJECT_ID`, `KAKERATTA_REGION`, `KAKERATTA_ZONE`, or ordinary
Terraform variables.
The script builds a versioned image, applies Firestore, Cloud Storage, and Cloud
Run infrastructure, deploys Firebase Hosting, and waits for a valid HTTPS
response at `https://PROJECT_ID.web.app`.

## Custom domain

Terraform creates a Cloud DNS zone for `kakeratta.net` and an A record pointing
at the fixed VM address. Configure the four values printed by the following
command as the domain's name servers at the registrar:

```sh
cd infra
terraform output name_servers
```

After delegation propagates, Caddy automatically obtains HTTPS certificates
for `kakeratta.net` and `www.kakeratta.net`.
After deployment, retrieve the private first-login URL with:

```sh
cd infra
terraform output -raw login_url
```

Terraform state contains the generated access token and must be kept private.
The data disk has `prevent_destroy`; remove that lifecycle rule deliberately
before destroying the disk and its data.

To inspect the VM startup process:

```sh
gcloud compute ssh kakeratta --zone=asia-northeast1-b \
  --command='sudo journalctl -u google-startup-scripts.service; sudo docker ps'
```
