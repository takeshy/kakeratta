# Google Cloud deployment

[日本語](README_ja.md)

This Terraform configuration runs one kakeratta instance on Compute Engine. A
separate persistent disk keeps the timeline, captures, settings, and persona
memories. Artifact Registry stores the container image, Secret Manager stores
the access token, and Caddy obtains HTTPS for the VM's fixed `sslip.io` name.

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
The script builds a versioned image, applies the infrastructure, restarts the
VM, and waits for a valid HTTPS response.

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
