---
type: "Process"
title: "Development and workspace operations"
description: "Development, validation, model usage inspection, deployment, memory migration, location backfill, portable exports, and workspace deletion."
tags: ["development", "usage", "deployment", "migration", "location", "export", "destroy"]
updated: "2026-10-06"
---

Development, validation, model usage inspection, deployment, memory migration, location backfill, portable exports, and workspace deletion.

# Development, validation, and deployment

## Development units

`Taskfile.yml` groups API build and startup, Vite, Storybook, frontend builds, and tests. The default API address is `127.0.0.1:8787`. Vite serves the frontend and proxies development `/api` requests to the API.

`frontend/build.mjs` creates distribution files by copying into `frontend/dist`. The browser loads ES modules directly; the application is not bundled. The build also assembles public assets, the landing page, manual, screenshots, and the Service Worker cache manifest.

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

After deploying research model separation and dynamic read capabilities, compare a deep or AlwaysUltra turn by its `trigger`: `purpose=research` and schedule/action planning should use the persona model, while `purpose=answer` uses the high model. Inspect workflow `reads` for requests made after Web search was exhausted, and compare research-round counts and token usage. API `outcome=success` does not mean a requested tool ran: a read may have received a host refusal. Keep before/after measurements separate; the [2026-10-05 production usage report](../production-model-usage-2026-10-05.md) describes the earlier behavior and a fixed-token cost estimate, not a post-deployment result.

## Deployment

The Dockerfile builds the Go binary and runs the API or push worker according to `KAKERATTA_MODE`; the frontend is a separate static artifact. `cloudbuild.yaml` builds and pushes an immutable image, builds the frontend beside it, uploads a frontend archive to the private shared bucket, then updates both containers through IAP/OS Login. The larger build worker still keeps fresh Go compilation short. The host serializes deployments, drains existing processes, checks startup and restores the previous images on failure. Nginx serves the static files and proxies API/SSE and authenticated internal pushes. See [deployment and rollback](../../infra/shared-vm/README.md).

The Kakeratta build account has object creation on the shared configuration bucket plus conditional object viewing restricted to `frontend/`. `gcloud storage cp` needs `storage.objects.get` for its destination check as well as creation for the upload; creation alone fails with 403. Terraform manages these bindings in `frontend_upload` and `frontend_upload_read`. The host bootstrap account separately reads the bucket's bootstrap bundles and frontend archives. See [shared VM Terraform](../../infra/shared-vm/main.tf).

Cloud Run remains the preferred hosted configuration. The shared VM is a temporary arrangement to reduce the cost of keeping production unchanged through the roughly one-month judging wait after the 2026-10-31 submission deadline.

`infra/shared-vm` defines the 4 GiB VM and its backends; GemiHub's existing Terraform module owns the retained HTTPS front end and URL map. Each application uses its original runtime service account, Firestore and storage. The VM has fixed capacity; it does not scale to zero or fail over to another instance. Geo MCP exposes place search and reverse geocoding directly at `https://geo.mcp.takeshy.work/mcp` with the existing API key; routing and API Gateway are removed.

Cloud Tasks now calls `https://kakeratta.net/internal/due` using the original app account; Pub/Sub calls `/internal/work`, and Scheduler calls `/internal/dispatch`. `KAKERATTA_TASKS_QUEUE`, `KAKERATTA_TASKS_URL`, `KAKERATTA_TASKS_ACCOUNT`, worker URL and dispatch URL must agree with those callers' token audiences. Durable work remains in Firestore and execution/claim semantics are unchanged. The old timer queue is paused, and retained Cloud Run services use the new endpoints for remaining fallback traffic. `firebase.json` still defines the legacy Firebase Hosting fallback. `shared_vm_ip` preserves migrated DNS and push endpoints in the original Terraform module. Firestore TTL policies on `expireAt` remain unchanged.

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

## Initial research planning model checks

Ordinary `go test ./internal/persona` covers planning schemas, model selection, budget expansion without resetting counters, plan retention, and bounded malformed-plan corrections using local fake completions. To check actual Super judgement, opt in with `KAKERATTA_RESEARCH_PLAN_LIVE=1 KAKERATTA_ROUTE_KEY_FILE="$PWD/tmp/token_factory.key" go test ./internal/persona -run TestInitialSuperPlanningLive -count=1 -v`. The [live test](../../internal/persona/research_plan_live_test.go) sends one Super research request per synthetic scenario (detail request, thanks, simple forecast question/follow-up, and complex comparison), consumes API credits, and performs no production reads or writes. These model checks are skipped unless explicitly enabled. On 2026-10-05, the five scenarios passed three runs each (15 first-round Super requests) with Web-search schemas offered and tool execution stubbed. Contextual detail and complex comparison selected deep; thanks and simple weather questions/follow-ups did not.

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
