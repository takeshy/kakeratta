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
Firebase authentication is required for deployment.

## Deploy

Requirements: an authenticated `gcloud` CLI, Terraform 1.7 or later, Node.js,
`jq`, and a GCP project with billing enabled. The current account needs
permission to manage project services, Cloud Run, Cloud Tasks, Firestore,
Cloud Storage, Firebase Hosting, Cloud DNS, IAM, Artifact Registry, and Secret
Manager.

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

After the initial infrastructure deployment, pushes to `main` are deployed by
the `kakeratta-main-deploy` Cloud Build trigger. The build updates the existing
Cloud Run service and Firebase Hosting; it does not apply Terraform or replace
runtime configuration. The GitHub repository connection and trigger are created
once in Cloud Build because connecting GitHub requires an OAuth installation.

### Tools: MCP servers

MCP servers are configured per deployment, not per user. Put them in
`infra/terraform.tfvars` (not committed) or pass them as `KAKERATTA_MCP_SERVERS`
to `deploy.sh`:

```hcl
mcp_servers = [
  { name = "geo", url = "https://geo.example.com/mcp", headers = { Authorization = "Bearer …" } },
]
```

For the geo-home-mcp server next to this repository, `./infra/link-geo-home.sh`
writes that block from geo-home-mcp's Terraform outputs (`mcp_url` and the
`mcp_api_key` bearer token), so the key is never copied by hand. Set
`GEO_HOME_DIR` if the checkout is elsewhere.

This becomes `KAKERATTA_MCP_SERVERS` on the Cloud Run service. It is marked
sensitive because headers may carry tokens, and ends up
in the Terraform state, so keep the state private. Each user's Tavily key for web
search is not here: users enter it in their own Settings.

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

Open the app URL and sign in with Google.

Backups (Settings → Backup) are written into the same bucket under `exports/`
and handed to the browser as a signed link that lasts an hour, so a download
never runs through Cloud Run. A lifecycle rule deletes anything under that
prefix after a day, and the runtime service account can sign for itself
(`roles/iam.serviceAccountTokenCreator`), which is what makes the link work.

Deletion is guarded where data lives: the captures bucket has
`prevent_destroy`, Firestore has delete protection, and the Cloud Run service
has `deletion_protection`. Remove those deliberately, not to get an apply
through.

To read the backend logs:

```sh
gcloud run services logs read kakeratta --region=asia-east1 --project=kakeratta
```

## Firestore indexes

Before updating the API, deploy the indexes in `firestore.indexes.json` and wait for them to become ready. They let Firestore fetch 20 parent posts without reading the whole timeline, and back semantic search with a vector index.

```sh
npx firebase-tools deploy --only firestore:indexes --project kakeratta
```

## Bluesky connection

Terraform generates the secret used for OAuth signing and connection encryption, stores it in Secret Manager, and passes it to Cloud Run as `KAKERATTA_BLUESKY_KEY`. The key also lives in Terraform state; keep that private and preserve the existing key. `KAKERATTA_PUBLIC_URL` defaults to `https://<domain>`. Set `bluesky_public_url` to use another public origin.

Update the server, Terraform resources, and frontend. Each user authorizes their own account on Bluesky; users do not need developer registration or API keys. See [operator setup](../docs/social-sharing.md) for public metadata routing, same-tab authorization, key management, and recovery after partial failure.
