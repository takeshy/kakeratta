---
type: "Process"
title: "Development and workspace operations"
description: "Development, validation, Alexa skill setup, model usage inspection, remote Terraform state and variables, deployment, memory migration, location backfill, portable exports, and workspace deletion."
tags: ["development", "usage", "deployment", "migration", "location", "export", "destroy"]
updated: "2026-10-15"
---

Development, validation, model usage inspection, remote Terraform state and variables, deployment, memory migration, location backfill, portable exports, and workspace deletion.

# Development, validation, and deployment

## Firebase redirect authentication

Production Web sets `KAKERATTA_FIREBASE_AUTH_DOMAIN=kakeratta.net`. [Nginx](../../infra/shared-vm/nginx.conf) transparently proxies `/__/auth/` GET/POST requests to `https://kakeratta.firebaseapp.com`, preserving paths and query strings, setting the upstream Host and TLS SNI, and verifying the upstream certificate. Helper responses are not cached and auth callback access logging is disabled. This avoids the cross-origin storage failure that returns Alexa users to login after Google authentication. The Firebase authorized domains must include `kakeratta.net`, and the Google OAuth web client must allow `https://kakeratta.net/__/auth/handler`; retain existing redirect URIs for older clients. See [Firebase redirect guidance](https://firebase.google.com/docs/auth/web/redirect-best-practices).

Install the updated Nginx configuration in both `/etc/nginx/nginx.conf` and `/opt/shared-apps/nginx.conf`, validate with `nginx -t`, and reload Nginx before changing the runtime auth domain. Back up configuration before editing; the normal image deployment does not update Nginx. After restarting Web with the new auth domain, persist the runtime manifest to a new version of `shared-apps-runtime` without displaying credentials. A fresh VM bootstrap bundle must also contain the updated Nginx configuration. Verify the public auth configuration, helper HTML/JavaScript and Google OAuth redirect acceptance, then complete a real Alexa account link. HTTP checks alone do not verify the browser's stored authentication state.

## Development units

Alexa custom-skill setup is described in [alexa.md](../alexa.md), including separate interaction models for Japanese and English in the US, UK, Canada, Australia and India. All locales share the endpoint and OAuth client, with every regional Console redirect URL allowlisted. Publication additionally requires locale descriptions and distribution settings. A Firebase-authenticated Web/combined process requires `KAKERATTA_ALEXA_SKILL_ID`, `KAKERATTA_ALEXA_CLIENT_ID`, `KAKERATTA_ALEXA_CLIENT_SECRET`, and whitespace-separated exact `KAKERATTA_ALEXA_REDIRECT_URLS`. Partial configuration or enabling Alexa without Firebase fails startup. With all values absent Alexa is disabled. Deployment environment/secret definitions and the Amazon Console must be configured separately; this implementation does not automatically register or deploy a skill. Signature, OAuth and speech tests use local fixtures; model-build and real account-linking/device checks remain deployment validation.

`Taskfile.yml` groups API build and startup, Vite, Storybook, frontend builds, and tests. The default API address is `127.0.0.1:8787`. Vite serves the frontend and proxies development `/api` requests to the API. Storybook starts on port 6106 with `mise exec -- npm --prefix frontend run storybook`.

`frontend/build.mjs` creates distribution files by copying into `frontend/dist`. The browser loads ES modules directly; the application is not bundled. The build also assembles public assets, the landing page, manual, privacy policy (`/privacy/`), terms (`/terms/`), screenshots, and the Service Worker cache manifest.

`mise exec -- npm --prefix frontend run captures` retakes the Japanese and English documentation screenshots from a fresh static Storybook build in a temporary directory. It requires Chromium (`CHROMIUM` can select the executable); `CAPTURE_SCRATCH` selects its profile directory, `CAPTURE_PORT` and `CAPTURE_DEBUG_PORT` select temporary local ports, and `CAPTURE_ONLY` selects comma-separated screenshot names. The capture server explicitly binds to `127.0.0.1`, and story API mocks pass non-API asset requests through to Storybook. Each shot uses a fresh browser tab and waits for its story content instead of a page-load notification, with bounded DevTools requests to avoid an indefinite capture run. Images are written to `docs/captures/{ja,en}/` at a 390px viewport and 2× scale, including the Storybook offline/pending fixture. Review the images and refresh referenced dimensions when they change.

Implementation: [captures.mjs](../../frontend/captures.mjs), [Screen.stories.js](../../frontend/stories/Screen.stories.js), and [story API fixtures](../../frontend/stories/api.js). Verification: [storybook-api.test.js](../../tests/storybook-api.test.js) checks that preview asset requests survive repeated fixture setup; [site-pages.test.js](../../tests/site-pages.test.js) checks that screenshot references and dimensions match the generated PNGs.

## Validation

```sh
go test ./...
node --test tests/*.test.js
node frontend/build.mjs
```

Go tests cover persistence, HTTP, personas, search, and integrations. Firestore tests, including the coordination and two-instance tests, run against the Firestore emulator when `FIRESTORE_EMULATOR_HOST` is set (for example `firebase emulators:start --only firestore`) and are skipped otherwise. `mise.toml` pins Go 1.26 and Node 24, matching the Dockerfile and Cloud Build. Frontend tests exercise state transitions and DOM operations under Node. Storybook provides screen examples, and `offline-check.mjs` checks offline behavior in a browser. `npm --prefix frontend run test:html` runs [html-check.mjs](../../frontend/html-check.mjs) against a local fixture server and headless Chromium to verify HTML isolation and image pagination; `CHROMIUM` selects the executable. Its temporary profile is created under the home cache by default for Snap compatibility and removed after the check. Real model providers, OAuth, microphones, and similar integrations require checks beyond unit tests.

WeatherAPI unit tests use a local mock provider. The opt-in live check consumes one provider request and loads the operator key without printing it:

```sh
KAKERATTA_WEATHERAPI_KEY_FILE="$PWD/tmp/weatherapi.key" KAKERATTA_WEATHERAPI_LIVE_TEST=1 go test ./internal/tools -run '^TestWeatherForecastLive$' -v
```

The retained Cloud Run Web and worker definitions resolve `KAKERATTA_JUDGE_MCP_KEY` from the existing Secret Manager secret `kakeratta-judge-mcp-key`, version `latest`. The key must be 64 characters. `judge_mcp_key_secret_id` and `judge_mcp_key_secret_version` select the reference; Terraform neither creates nor reads the secret value. The existing runtime account has Secret Manager accessor permission. For the active shared VM, configure the same reference under `secrets.KAKERATTA_JUDGE_MCP_KEY` in both Kakeratta app entries of `/opt/shared-apps/runtime.json`, remove any literal environment value, deploy both containers and persist the manifest to `shared-apps-runtime`. This VM configuration must be applied separately from Terraform. Nginx explicitly proxies `/internal/judge-workspaces` to the Web container; the endpoint requires the shared bearer key and an active judge period, while other unlisted internal paths remain blocked. See [operator setup](../../infra/README.md#shared-judge-mcp-key) and [VM deployment](../../infra/shared-vm/deploy.py).

Production resolves `KAKERATTA_WEATHERAPI_KEY` from the existing Secret Manager secret `kakeratta-wheatherapi-key`, version `latest`, when the VM deployment starts the containers. The spelling is the registered secret ID. The original runtime service account retains accessor permission. The operator manages secret versions; Terraform records only the initial runtime secret reference. The retained Cloud Run definition uses `weatherapi_secret_id` / `weatherapi_secret_version`. The VM manifest preserves the same reference, along with existing OAuth, VAPID, model and MCP configuration. A fresh deployment resolves it without printing the payload or putting it in process arguments. See [Weather forecasts](./integrations.md#weather-forecasts).

## Push worker operations

The split Cloud Run production deployment switched on 2026-10-04 through Cloud Build `eebc6834-f4d6-4748-af8f-2122608a94b3` and Terraform: Web revision `kakeratta-00223-pw9`, worker revision `kakeratta-worker-00001-vh8`. Both reported ready; worker resource settings matched Terraform. Pending legacy reactions were zero. Verification covered an authenticated Pub/Sub push, a Scheduler-triggered outbox repair returning 204, durable claiming and completion of an isolated orphan-routing reaction, and removal of its outbox and temporary workspace. No real model call or user entry was used. The public site and auth configuration endpoint returned 200, the scheduler was enabled without a last-attempt error, and existing Cloud Tasks alarms remained present. Initial push attempts returned 403 during IAM propagation before subsequent attempts succeeded.

Terraform enables `queue_worker_enabled` by default. Deploy the queue-aware image and apply Terraform with that image tag. This creates the private worker, Pub/Sub topic/subscription and IAM, the `dispatchOutbox.readyAt` index, and a minute Cloud Scheduler repair job. Wait for service revisions and the index to become ready, then confirm successful Pub/Sub delivery and persisted routing/delivery results. Set `queue_worker_enabled = false` to use the combined runtime instead. See [Terraform](../../infra/worker.tf).

No legacy queue migration is included for the current single-user deployment. Finish pending work on the existing combined service before switching. Existing entries, settings and persona memory keep their storage format. Any pending reaction without a dispatch generation will not be picked up by push workers; no queue data is deleted automatically.

The production deployment moved to the shared VM on 2026-10-06. Pub/Sub subscription and Scheduler repair now use `kakeratta.net` endpoints; 19 scheduled HTTP requests were copied from `kakeratta-due` into `kakeratta-vm-due`, preserving bodies and times while changing URL/token audience. The old queue stays paused for rollback. Current web/worker processes keep the split-mode persistence and claim semantics.

In the retained Cloud Run configuration, worker CPU, memory and instance limits are managed in `infra/worker.tf` and `infra/variables.tf`; increase Web and worker capacity independently as measured demand grows. It requires no waiting instance, but actual requests, Pub/Sub, Firestore and Scheduler can add charges. The minute repair request/query occurs even when idle. Web retains its existing CPU/memory configuration because other handlers can start background tasks. Terraform defines the retained Cloud Run worker; the current Cloud Build pipeline deploys the shared VM containers and frontend instead.

Monitor memory exits, eight-minute cancellations, redelivery, oldest unacknowledged message age, due outbox growth, and scheduler failures. Check resource sizing against real provider workflows. Unacknowledged messages are retained seven days; this initial configuration has no dead-letter subscription. After retention expires, inspect pending reactions and explicitly reissue dispatch requests: unpublished-only repair cannot recover expired published messages. Real-provider latency, long workflows and Firestore load need measurement before claiming large-user capacity. Split modes require `KAKERATTA_JOB_TOPIC`, `KAKERATTA_JOB_SUBSCRIPTION`, `KAKERATTA_WORKER_URL`, and `KAKERATTA_DISPATCH_URL` in addition to the existing Firestore, GCS and Cloud Tasks settings.

## Inspecting model usage

Read current structured inference records from both VM containers in `geminihub-486523`; records before the VM migration remain under the Cloud Run resources in project `kakeratta`:

```sh
gcloud logging read 'resource.type="gce_instance" AND labels.service_name:"kakeratta" AND jsonPayload.path="model-usage"' --project=geminihub-486523 --freshness=1d --limit=10000 --format=json
```

Fix the date range and timezone when reconciling provider billing, and check that the result limit was not reached. Group by `purpose`, `model`, and `wake` to distinguish post-triggered work from autonomous check-ins. Sum `usage.prompt_tokens` and `usage.completion_tokens` only when reported; count missing values separately. Count records for observed requests and use retry fields to identify correction, phase, note-job and reaction-job scopes. Reported reasoning/cache details are breakdowns, not extra counts to add to the totals. External agents and development traffic billed directly to the same provider account require separate reconciliation. The [backend logging specification](./backend.md#model-usage-logging) defines fields and failure semantics; records begin with the deployment and cannot reconstruct earlier usage.

Hermes usage is recorded separately inside its sandbox, currently in `/sandbox/.hermes/runtime/state.db` under `session_model_usage`. Reconcile it by model and session against the provider's billing-day boundary; do not assume that a provider's “yesterday” means JST. The 2026-10-13 investigation found 462 calls in the JST calendar day but 594 calls in the provider-aligned UTC day. Hermes records ordinary input, cache reads, output, and reasoning as separate fields for this provider, while its USD cost fields remain unknown/zero. Calculate only an explicitly labelled estimate from the contemporaneous provider rates, and compare it with the provider total rather than presenting the local USD fields as billed cost. Group by session as well as model: repeated 28–34-call Super sessions exposed context re-sending that a model-level total alone obscured. See the [direct-request worker cost analysis](../direct-request-worker-spec_ja.md#2026-10-13-token-factory%E8%B2%BB%E7%94%A8%E5%88%86%E6%9E%90).

After deploying aggregate Super routing and combined answers, group `purpose=routing` by trigger: automatically routed posts use one request for all subject conditions, while mentions and active-persona replies bypass routing. Routing call counts therefore cannot serve as total post counts. Count user entries from the saved timeline, including silent/no-recipient posts. Compare normal/deep/AlwaysUltra turns by trigger: ordinary turns record reads and the answer as `purpose=answer` on middle; deep/AlwaysUltra turns of personas with lookup tools record Super reads and `finish_read` as `purpose=research` and one Ultra `purpose=answer`; the infographic records reads and the answer as Ultra `purpose=answer`. Schedule planning remains on the middle model. Inspect workflow reads for host refusals and remaining budgets. The internal day's summary appears as `purpose=daily_summary`, written once per account day before the check-ins, and is shared by every persona's check-in. Quiet daily check-ins log `check-in skipped` and make no model call; count them beside the check-ins that ran. Keep periodic check-ins and monthly summaries separate when measuring cost per user post, and treat infographics as a separate workload. API success does not prove successful tool execution or publication. Compare measured token totals and model rates, without assuming cache discounts; historical reports describe their original implementation and window.

## Terraform state and variables

The app module (`infra/`) and shared VM module (`infra/shared-vm/`) use a GCS backend in the private `takeshy-work-kakeratta-terraform` bucket in project `takeshy-work`. Their state prefixes are `state/app` and `state/shared-vm`, each with `default.tfstate`. The bucket has uniform access, enforced public access prevention and object versioning. It is bootstrapped separately from these modules so state storage does not depend on applying the resources it tracks. Terraform provides state locking. The bucket is outside the app/VM projects so their project-level build permissions do not automatically grant state access; operators use their existing authorized Google identity.

Private variables are versioned objects under `vars/app/terraform.tfvars`, `vars/app/shared-vm.auto.tfvars` and `vars/shared-vm/terraform.tfvars`. [terraform.py](../../infra/terraform.py) runs a selected module: `python3 infra/terraform.py shared-vm init`, `... shared-vm plan -out=/absolute/path/change.tfplan`, then `... shared-vm apply /absolute/path/change.tfplan`. Replace `shared-vm` with `app` for the original module. Plan and unsaved apply download the module's variables into a private temporary directory, pass them with `-var-file`, and clean them up on completion or failure. Saved-plan apply uses the variables already captured in that plan. Explicit CLI variable options still take precedence. `fetch-vars` retrieves editable local copies and refuses to overwrite existing files unless `--force` is explicit. `upload-vars` publishes the module's local ignored tfvars after an operator edits them; ordinary plan/apply never upload variables. The retained deployment script uses the same wrapper.

The wrapper uses existing credential environment variables when provided; otherwise it obtains the logged-in gcloud account's short-lived token and passes it only through `GOOGLE_OAUTH_ACCESS_TOKEN`. Credentials are absent from backend blocks and command arguments. State, tfvars and saved plans may contain secrets; they remain outside version control. Runtime Cloud Build does not run Terraform or download these variables.

Git ignores local state, plans and HCL/JSON tfvars. `.gcloudignore` includes those Git exclusions for source uploads, and `.dockerignore` excludes Terraform working directories, state, plans and variables recursively from image build contexts. Verification: [test_terraform.py](../../infra/test_terraform.py) covers private temporary variable cleanup after a failed plan, saved-plan application without fetching variables, and preserving local files after a failed variable fetch.

The 2026-10-06 migration backed up both local states and all three variable files, copied variables to GCS, and ran `init -migrate-state -force-copy` separately for each module. Verification compares the original state lineage, resource data and outputs, and checks variable files byte for byte. The app's private variables also retain Firebase browser configuration, the retained Cloud Run image tag and the judge model key previously supplied through deployment CLI options, so standalone plans do not silently clear those values. Existing local state/backup files are recovery snapshots; GCS is the active backend. Restore an inspected historical state through Terraform's state tooling rather than overwriting an active state object. See the [GCS backend specification](https://developer.hashicorp.com/terraform/language/backend/gcs).

## Deployment

The Dockerfile builds the Go binary and runs the API or push worker according to `KAKERATTA_MODE`; the frontend is a separate static artifact. `cloudbuild.yaml` builds and pushes an immutable image, builds the frontend beside it, uploads a frontend archive to the private shared bucket, then updates both containers through IAP/OS Login. The larger build worker still keeps fresh Go compilation short. The host serializes deployments, drains existing processes, checks startup and restores the previous images on failure. Nginx serves the static files and proxies API/SSE and authenticated internal pushes. See [deployment and rollback](../../infra/shared-vm/README.md).

For manual Cloud Build submissions, supply `_IMAGE` and `_VERSION` and use the existing build service account. The YAML declares only substitutions consumed by its steps; obsolete Cloud Run/WeatherAPI substitutions were removed because strict manual submissions reject unused keys. VM secrets continue to come from the runtime manifest.

The Kakeratta build account has object creation on the shared configuration bucket plus bucket listing and conditional object viewing restricted to `frontend/`. `gcloud storage cp` needs destination checking (`storage.objects.get`) and bucket listing (`storage.objects.list`) as well as creation for the upload; creation alone fails with 403. Terraform manages these bindings in `frontend_upload` and `frontend_upload_read`. Bootstrap bundle contents remain outside the build account's read condition. The host bootstrap account separately reads the bucket's bootstrap bundles and frontend archives. See [shared VM Terraform](../../infra/shared-vm/main.tf).

Cloud Run remains the preferred hosted configuration. The shared VM is a temporary arrangement to reduce the cost of keeping production unchanged through the roughly one-month judging wait after the 2026-10-31 submission deadline.

`infra/shared-vm` defines the 4 GiB VM and its backends; GemiHub's existing Terraform module owns the retained HTTPS front end and URL map. Each application uses its original runtime service account, Firestore and storage. The VM has fixed capacity; it does not scale to zero or fail over to another instance. Geo MCP exposes place search and reverse geocoding directly at `https://geo.mcp.takeshy.work/mcp` with the existing API key; routing and API Gateway are removed.

The Kakeratta server in [nginx.conf](../../infra/shared-vm/nginx.conf) uses `absolute_redirect off`. Directory redirects such as `/lp` to `/lp/` and `/manual` to `/manual/` therefore return relative locations, preserving the browser's public HTTPS origin rather than exposing the internal HTTP listener on port 8080. When updating this host configuration, preserve both `/opt/shared-apps/nginx.conf` (used on bootstrap) and `/etc/nginx/nginx.conf` (active), back up the current files, run `nginx -t`, then reload Nginx. Verify the public redirect locations and final 200 responses after applying.

Cloud Tasks now calls `https://kakeratta.net/internal/due` using the original app account; Pub/Sub calls `/internal/work`, and Scheduler calls `/internal/dispatch`. `KAKERATTA_TASKS_QUEUE`, `KAKERATTA_TASKS_URL`, `KAKERATTA_TASKS_ACCOUNT`, worker URL and dispatch URL must agree with those callers' token audiences. Durable work remains in Firestore and execution/claim semantics are unchanged. The old timer queue is paused, and retained Cloud Run services use the new endpoints for remaining fallback traffic. `firebase.json` still defines the legacy Firebase Hosting fallback. `shared_vm_ip` preserves migrated DNS and push endpoints in the original Terraform module. Firestore TTL policies on `expireAt` remain unchanged.

Both retained Cloud Run services select the same alarm destination from `shared_vm_ip`: a nonempty value selects `kakeratta-vm-due` and `https://kakeratta.net/internal/due`; an empty value selects the original `kakeratta-due` queue and Cloud Run Web due URL. The worker's `custom_audiences` follows `local.worker_url`, matching the Pub/Sub token audience. Updating the retained services with Terraform therefore preserves the active VM alarm path rather than restoring the paused legacy queue. Implementation: [Web configuration](../../infra/main.tf) and [worker configuration](../../infra/worker.tf).

## Web Push configuration

Generate a VAPID pair once with `go run ./cmd/webpush-keys` ([main.go](../../cmd/webpush-keys/main.go)). Store the private value securely, and configure the same pair on every API instance:

- `KAKERATTA_PUSH_PUBLIC_KEY`: base64url P-256 public key.
- `KAKERATTA_PUSH_PRIVATE_KEY`: base64url private key; keep this in the deployment's secret configuration.
- `KAKERATTA_PUSH_SUBJECT`: operator contact as `mailto:operator@example.com` or an HTTPS URL.

Without all three valid values, the push API reports unavailable and scheduled timeline posts continue normally. Terraform supplies the public key and contact from `push_public_key` and `push_subject`, defaulting to the generated key and `mailto:takeshy.work@gmail.com`. It maps `KAKERATTA_PUSH_PRIVATE_KEY` to the existing Secret Manager secret named by `push_private_key_secret` (default `kakeratta-vapid-private`) and version `push_private_key_version` (default `latest`). The operator creates the secret and its value; Terraform references it without retrieving the private key into state. The runtime service account already has `roles/secretmanager.secretAccessor`. For the retained Cloud Run configuration, apply Terraform service changes before updating its image. On the VM, deployment resolves secret references from `/opt/shared-apps/runtime.json`; update the VM manifest when changing configuration. The current Cloud Build pipeline preserves that manifest configuration. Keep keys stable across releases; rotation requires users to disable and re-enable notifications. Build and deploy the frontend alongside the API so the updated `/sw.js` is served. Users enable notifications under **This device** in settings; browser permission alone does not register a device. HTTPS and a supported browser are required; iOS/iPadOS users open the app from the Home Screen.

Web Push uses [webpush-go](https://github.com/SherClockHolmes/webpush-go) for payload encryption and VAPID signing. Unit tests stub providers; actual device delivery, browser/OS permissions, and background behavior require a configured deployment and a real-device check. Delivery is best effort without automatic server retries; identity verification also suppresses notifications when login has expired or the API cannot be reached.

## Memory migration

Persona fields moved from prose rewritten with every kakera to a list of facts with a summary rewritten at the check-in, and the running app reads no facts from prose. Split the prose once after deploying that change with [cmd/migrate-memory](../../cmd/migrate-memory/main.go), which runs `Runner.MigrateMemory` for every persona of a workspace on that workspace's own model settings:

- `go run ./cmd/migrate-memory` - the local workspace (`KAKERATTA_HOME` or the default); `-firestore kakeratta` - every workspace in production Firestore with application default credentials, or one with `-user KEY`; `-persona ID` limits it to one persona.
- Without `-apply` it prints, per persona, the prose fields, the prose each named field keeps as its summary, and the facts the model splits it into, and saves them to `-plan` (JSON, default `migration-plan.json`); nothing is changed. The plan may be edited by hand.
- With `-apply` it writes the plan as it is, without calling the model again (`Runner.ApplyMigration`). A persona whose prose changed since the dry run, or whose plan holds an unknown field, an over-long fact or summary, or an overfull field, is refused. Running the dry run again afterwards finds nothing to do.
- Between the deploy and the run, a prose field reads as its summary, with no facts yet. A dry run whose prose was rewritten meanwhile is refused at `-apply` and must be run again.
- The production split of 2026-10-02 was done this way: two dry runs (the first showed a submission rejected without its reasons, and bullets carried into facts, both since fixed), a hand edit of the plan for a field the model split short, then `-apply`.
- The earlier move from one OKF note per thing to prose fields (2026-10-01) was done the same way: three dry runs while tightening the prompt (no researched figures or explanations; the user's actions kept with their dates; the notes' own language), a hand edit of the plan, then `-apply`.

## Backfilling post locations

[cmd/backfill-places](../../cmd/backfill-places/main.go) adds missing labels to existing Firestore posts using the configured `reverse_geocode` MCP tool. It requires application default credentials and an explicit `-firestore PROJECT`; optional `-user KEY` limits the workspace, otherwise it scans all `users/{key}/timeline` collections in the default database. By default, only undeleted user posts with valid coordinates and an empty location label are eligible. Explicit `-refresh` also includes existing approximate geocoded labels, allowing localized municipality names to replace older labels; custom non-approximate labels remain unchanged. Unchanged lookup results are omitted from the plan. The `-metadata-only` option includes approximate geocoded entries while preserving their current display labels, adding country and region fields for search. Lookup calls allow 60 seconds. Metadata-only plans also record `metadataOnly: true`. Refresh plans record `refresh: true`; applying existing-label changes without that marker is rejected. It does not infer coordinates from text. Lookups pass each post's saved `language` (`en` or `ja`, defaulting to `ja`) and use [geocode.Resolve](../../internal/geocode/place.go) to combine localized municipality/district names (administrative levels 2–4) with the settlement name. The hierarchy describes the nearest settlement, not coordinate containment. Older responses without the hierarchy remain supported.

1. `go run ./cmd/backfill-places -firestore PROJECT -scan` counts entries and eligible posts without MCP calls or writes.
2. `go run ./cmd/backfill-places -firestore PROJECT -mcp-config PRIVATE.json -plan PLAN.json` reads an operator MCP server array in the same format as `KAKERATTA_MCP_SERVERS`, resolves locations sequentially with a 60-second timeout per call, and saves a plan without modifying Firestore. The plan includes document paths, update times, and before/after location metadata. It is created with mode `0600` and must not already exist; keep both files outside version control because they contain credentials or coordinates. A no-match result is counted and omitted; lookup errors abort without applying changes.
3. `go run ./cmd/backfill-places -firestore PROJECT -plan PLAN.json -apply` applies the saved labels without further MCP calls. It checks the plan's project and paths, forbids coordinate changes, and updates only `Place.Label`, `Place.Approximate`, `Place.GeonameID`, `Place.Country`, `Place.CountryCode`, and `Place.Region`, conditioned on the document's exact dry-run update time. Changed or deleted documents are skipped. An interrupted apply can be rerun: already updated documents fail the original precondition and are skipped. Generate a new plan for remaining eligible posts when needed.
4. `go run ./cmd/backfill-places -firestore PROJECT -plan PLAN.json -verify` reads each planned document back and compares its location with the plan. Missing or different locations produce a nonzero exit. Repeat `-scan` to count remaining eligible posts.

The command does not reroute posts, wake personas, rewrite text or timestamps, or emit live events. Reload the timeline to read the saved labels. After deploying the search-text change, the next index pass or search rebuilds affected embeddings from the saved country and region fields. Use `-refresh` with `-scan` and when generating a plan to update earlier geocoded labels. The same apply/verify steps and update-time preconditions apply. New posts use durable asynchronous lookup and retry; see [post location labels](./backend.md#post-location-labels). Verification: [main_test.go](../../cmd/backfill-places/main_test.go) covers candidate selection, malformed/no-match responses, and plan validation.

## Implementation and verification

- [Taskfile.yml](../../Taskfile.yml)
- [vite.config.js](../../frontend/vite.config.js)
- [build.mjs](../../frontend/build.mjs)
- [Dockerfile](../../Dockerfile)
- [cloudbuild.yaml](../../cloudbuild.yaml): image/frontend build and shared VM deployment
- [shared VM Terraform](../../infra/shared-vm/main.tf): host, backends and timer queue
- [deploy.py](../../infra/shared-vm/deploy.py): serialized container deployment, startup checks, rollback and frontend archive validation
- [control.py](../../infra/shared-vm/control.py) and [test_control.py](../../infra/shared-vm/test_control.py): runtime identity broker and verification
- [nginx.conf](../../infra/shared-vm/nginx.conf): frontend, API/SSE and internal push routing
- [firebase.json](../../firebase.json)
- [main.tf](../../infra/main.tf): scaling, Cloud Tasks, VAPID secret references, permissions, and TTL policies
- [variables.tf](../../infra/variables.tf): Web Push public key, contact, secret name, and version
- [mise.toml](../../mise.toml)
- [README.md](../../infra/README.md)
- [migrate-memory](../../cmd/migrate-memory/main.go): one-off memory migration

## Routing and answer verification

Ordinary `go test ./internal/persona` covers aggregate Super routing, fixed keyword depth, combined read/answer calls, Super reads with Ultra answers for deep turns, actual lookup budgets and bounded corrections with local fake completions. [route_test.go](../../internal/persona/route_test.go) verifies aggregate responses, recipient normalization, exclusion reasons, configured model choices and Jev. [answer_loop_test.go](../../internal/persona/answer_loop_test.go) verifies that an answer without reads needs one call. [research_plan_test.go](../../internal/persona/research_plan_test.go) now covers the local string rule; the former paid Super planning test was removed with the model-based planner. These tests do not establish real-model classification quality or production savings.

# Workspace export and deletion

## Backup format

`Workspace.Export` converts state into the layout a local kakeratta reads and passes files to a sink in sequence. The HTTP layer streams a zip without assembling the entire workspace in memory. It includes posts, personas, memory, images, favorites, and related data.

Local timeline files and Firestore exports use `timeline/YYYY/MM/DD.json`: one JSON array of current entries per date. Firestore export streams entries in timestamp order and buffers one day at a time. Restoring the exported directory rebuilds the local timeline index on startup. Unfinished reactions, runtime records, locks, and events are runtime state and are not exported. Favorites are user-owned records: local exports retain `favorites/{entry-id}.json`, and Firestore exports materialize the same files for restoration.

Export settings omit API credentials, and Bluesky tokens and MCP connection keys are excluded as well. Connections must be configured again after restoration. The implementation does not simply zip the entire workspace directory.

## Delivery paths

With an external ExportTarget, `POST /api/export` starts asynchronous work. Completion sends the download location over SSE. The GCS target creates an expiring URL. Without an export target, `GET /api/export` streams the download directly. The asynchronous POST path allows one export per Server instance and sets a 30-minute deadline. This is an in-process limit, not a workspace-wide distributed lock: different instances can each start an export. The direct GET path uses the request context and has neither this concurrency gate nor an explicit 30-minute export deadline.

## Deletion

For hosted workspace deletion, Gateway accepts a confirmed request, takes the user out of service, halts the Runner and related workers, then calls `Destroy` and removes backups. It first marks the workspace closing so no reaction is claimed anywhere; with several instances it waits for work in hand elsewhere to end and deletes the workspace again after 90 seconds and 11 minutes (see [Account deletion](./architecture.md#account-deletion)). It subsequently removes the login and cookie, and the frontend clears device storage. A standalone local Server returns 409 for deletion and tells the user to stop the app and remove the workspace directory manually.

## Implementation and verification

- [export.go](../../internal/server/export.go)
- [export.go](../../internal/store/export.go)
- [exports.go](../../internal/store/exports.go)
- [destroy.go](../../internal/server/destroy.go)
- [destroy.go](../../internal/store/destroy.go)
- [export_test.go](../../internal/server/export_test.go)
- [destroy_test.go](../../internal/store/destroy_test.go)
- [cluster_test.go](../../internal/server/cluster_test.go): deletion requested through another instance

# Related chapters

- [Architecture and runtime](./architecture.md)
- [API and persistent state](./backend.md)
- [Browser application and device state](./frontend.md)

## Alexa certification account

[provision-alexa-review](../../cmd/provision-alexa-review/main.go) creates a new Firebase email/password account with the `judge` claim and an isolated Firestore workspace in the specified project. It refuses existing email accounts and existing output files. A randomly generated password is written only to a mode-0600 credentials file before creating the remote account, so partial failures retain the login information. It seeds a clearly labelled English example note/reply and a future example schedule (default 30 days ahead, overridable with `-schedule-at`), without routing the sample note to AI. The tool uses ADC or an operator access-token file and sets the quota project explicitly. Provisioning does not deploy the app, change runtime judge expiry/quota/key settings, or reset counters. Check those settings and sample schedule dates before submission; inspect any partial failure before retrying. See [review setup](../judge-access.md#alexa-certification).


The 2026-10-11 Alexa certification deployment used Cloud Build `18e7682e-8894-4aa3-92b6-7d7a8f44aea5` and revision `8b605c1`. Production checks covered the public legal pages, review login/session, example reply, authorization code single use, refresh and revocation, and unsigned-request rejection. [Submission instructions and the verification record](../judge-access.md#submission-instructions-and-final-checks) distinguish these checks from operator-reported Console settings and still-required device tests. Credential-free Japanese/English test instructions live in `integrations/alexa/testing-instructions-{ja,en}.txt`; filled credentials remain outside version control.
