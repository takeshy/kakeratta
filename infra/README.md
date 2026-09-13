# Google Cloud deployment

[日本語](README_ja.md)

The deployed app is two independent pieces. **Firebase Hosting** serves the
page — `frontend/dist`, which nothing else serves — and rewrites
`/api/**` to **Cloud Run**, which runs the Go binary with Firestore for the
timeline, settings and notes, and a private Cloud Storage bucket for captures.
Nothing is kept on an instance disk, so the page is a static site in front of a
stateless service, and a change to the page ships without rebuilding the image.

## Multi-user authentication

Create a Firebase Web app in the same Google Cloud project and enable Google
under **Firebase Authentication > Sign-in method**. The deploy script reads the
active Web app configuration directly from the Firebase Management API; no
local Firebase config file or API-key environment variable is needed.

The session cookie is named `__session` because Firebase Hosting strips every
other cookie before a request reaches Cloud Run; any other name logs in and is
then `login required` on the next call. Each signed-in user gets an isolated
store keyed by their Firebase `uid`.
Firebase authentication takes precedence over the shared `KAKERATTA_TOKEN`
mode, which is what a local single-user run uses instead.

## Deploy

Requirements: an authenticated `gcloud` CLI, Terraform 1.7 or later, Node.js,
`jq`, and a GCP project with billing enabled. The current account needs
permission to manage project services, Cloud Run, Firestore, Cloud Storage,
Firebase Hosting, Cloud DNS, IAM, Artifact Registry, and Secret Manager.

```sh
gcloud auth application-default login
./infra/deploy.sh
```

The defaults use project `kakeratta`, build in `asia-northeast1`, and run in
`asia-east1` — Cloud Run has to sit in a region Firebase Hosting can rewrite to.
Override with `KAKERATTA_PROJECT_ID`, `KAKERATTA_REGION`,
`KAKERATTA_RUN_REGION`, or ordinary Terraform variables.

The script builds a versioned image, applies the Terraform configuration, runs
`node frontend/build.mjs`, deploys Firebase Hosting, and waits for a valid HTTPS
response at `https://PROJECT_ID.web.app`.

A page-only change does not need the image:

```sh
node frontend/build.mjs
npx firebase-tools deploy --only hosting --project kakeratta
```

## Custom domain

Terraform creates the Cloud DNS zone for `kakeratta.net` and the three records
Firebase Hosting needs: an apex `A` record at Hosting's address, the
`hosting-site` `TXT` record that proves ownership, and `www` as a `CNAME` to
`kakeratta.web.app`. Register the four values printed below as the domain's
name servers at the registrar:

```sh
cd infra
terraform output name_servers
```

Once delegation propagates, add `kakeratta.net` under **Hosting > Custom
domains** in the Firebase console. Hosting issues and renews the certificate;
`https://kakeratta.web.app` works throughout and is the address to check while
DNS is still moving.

## After deploying

Without Firebase authentication configured, the app falls back to the shared
access token. The one-time login URL for that mode is:

```sh
cd infra
terraform output -raw login_url
```

Keep it private. Terraform state contains the generated token and must be kept
private for the same reason.

Deletion is guarded where data lives: the captures bucket has
`prevent_destroy`, Firestore has delete protection, and the Cloud Run service
has `deletion_protection`. Remove those deliberately, not to get an apply
through.

To read the backend logs:

```sh
gcloud run services logs read kakeratta --region=asia-east1 --project=kakeratta
```

## Left over from the VM deployment

The earlier single-VM setup (Compute Engine with Caddy terminating TLS on an
`sslip.io` name) is gone. Two of its resources are still declared —
`google_compute_address.app` and `google_compute_firewall.web` — and the
`fallback_url` output is still derived from that address, so it no longer points
at anything serving. Remove them once the Cloud Run path has been verified.
