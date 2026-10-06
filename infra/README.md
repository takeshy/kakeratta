# Google Cloud deployment

[日本語](README_ja.md)

## Current production: temporary shared VM

Cloud Run remains the preferred hosted configuration. To reduce the cost
of keeping production unchanged through the roughly one-month judging wait
after the 2026-10-31 submission deadline, production moved to a shared Compute
Engine VM on 2026-10-06. Web and worker remain separate containers; Firestore,
GCS, Firebase authentication and durable queue semantics remain in use.

The HTTPS load balancer and Nginx serve the static frontend and proxy API/SSE
and authenticated internal pushes. The VM also hosts GemiHub and Geo MCP and
has fixed capacity without automatic failover. Its worker stays running.

[Shared VM operations](shared-vm/README.md) covers provisioning, runtime
identities, deployment and rollback. The current `cloudbuild.yaml` builds the
image and frontend, uploads the frontend archive, and deploys both containers
through IAP/OS Login. Production logs are in `geminihub-486523`:

```sh
gcloud logging read 'resource.type="gce_instance" AND labels.service_name:"kakeratta"' --project=geminihub-486523 --freshness=1d --limit=100 --format=json
```

The following sections describe the retained Firebase Hosting/Cloud Run
configuration and `infra/deploy.sh`. They are not the current production
release procedure; Firebase Hosting updates do not update the VM frontend.
With `shared_vm_ip` set, the original Terraform module preserves the migrated
DNS and Pub/Sub/Scheduler endpoints. See the shared VM guide before changing
production configuration or switching timer queues.

## Cloud Run configuration

The deployed app separates the browser, Web API and job worker. **Firebase Hosting** serves the
page — `frontend/dist`, which nothing else serves — and rewrites
`/api/**` to **Cloud Run**, which runs the Go binary with Firestore for the
timeline, settings and notes, and a private Cloud Storage bucket for captures.
Nothing is kept on an instance disk, so the page is a static site in front of a
stateless service, and a change to the page ships without rebuilding the image.

## Push worker

Pub/Sub push processing is enabled by default through `queue_worker_enabled`.
It uses a separate scale-to-zero Cloud Run worker. CPU, memory and instance
limits are configurable in Terraform. Deploy the queue-aware image;
`worker_max_instances` can be increased later. See the
[operating procedure](../docs/system/operations.md#push-worker-operations).
Finish existing pending work before switching; no queue migration is included
for the current single-user deployment. Set `queue_worker_enabled = false`
to run the combined service instead.

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

The current `kakeratta-main-deploy` trigger targets the shared VM rather than
these Cloud Run/Firebase Hosting resources. During migration it uses an inline
configuration matching `cloudbuild.yaml`; changing the repository build file
also requires updating the inline trigger, or switching it to the published
file. See [shared VM deployment](shared-vm/README.md#deployment-and-recovery).

### Reminder Web Push

Terraform configures the generated VAPID public key and
`mailto:takeshy.work@gmail.com`, and references the existing Secret Manager
secret `kakeratta-vapid-private:latest` as `KAKERATTA_PUSH_PRIVATE_KEY`.
The operator creates the secret and stores its base64url private key; Terraform
does not read its value into state. The runtime account already has Secret
Manager access. Override `push_public_key`, `push_subject`,
`push_private_key_secret`, and `push_private_key_version` for another deployment.

Apply the Terraform change with the current image tag before an image-only
Cloud Build deployment; the main branch build preserves service environment
configuration. Keep the key pair stable across releases. For rotation, update
both keys together and ask users to disable and re-enable notifications.
Users enable birthday and appointment reminders under **This device** in
settings. See [Web Push configuration](../docs/system/operations.md#web-push-configuration).

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

## WeatherAPI

Create the Secret Manager secret `kakeratta-wheatherapi-key` in the deployment project and add the API key as an enabled version. This is the registered spelling. The retained Cloud Run Terraform configuration binds its `latest` version to the Cloud Run environment variable `KAKERATTA_WEATHERAPI_KEY`. The runtime account already has Secret Manager accessor permission. The key remains outside source code, Docker images, build substitutions, and Terraform state; Terraform only refers to the existing secret.

To select a different secret or pin a version, set Terraform's `weatherapi_secret_id` / `weatherapi_secret_version` for that Cloud Run deployment. For the VM, update the runtime manifest secret reference. A new Cloud Run instance loads the selected version on startup; existing instances retain their loaded value.

## Bluesky connection

Terraform generates the secret used for OAuth signing and connection encryption, stores it in Secret Manager, and passes it to Cloud Run as `KAKERATTA_BLUESKY_KEY`. The key also lives in Terraform state; keep that private and preserve the existing key. `KAKERATTA_PUBLIC_URL` defaults to `https://<domain>`. Set `bluesky_public_url` to use another public origin.

Update the server, Terraform resources, and frontend. Each user authorizes their own account on Bluesky; users do not need developer registration or API keys. See [operator setup](../docs/social-sharing.md) for public metadata routing, same-tab authorization, key management, and recovery after partial failure.
