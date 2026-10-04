---
type: "Process"
title: "Development and workspace operations"
description: "Development, validation, model usage inspection, deployment, memory migration, location backfill, portable exports, and workspace deletion."
tags: ["development", "usage", "deployment", "migration", "location", "export", "destroy"]
updated: "2026-10-05"
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

Production deployment binds `KAKERATTA_WEATHERAPI_KEY` to the existing Secret Manager secret `kakeratta-wheatherapi-key`, version `latest`, in both the Terraform Cloud Run definition (`weatherapi_secret_id` / `weatherapi_secret_version`) and Cloud Build deployment (`_WEATHERAPI_SECRET` / `_WEATHERAPI_SECRET_VERSION`). The spelling is the registered secret ID. The runtime service account already has Secret Manager accessor permission. The operator manages secret versions separately; no WeatherAPI secret payload is read or created by Terraform, and only the secret reference enters state. A new instance resolves the configured version on startup. Keep both deployment paths aligned when overriding the secret ID or version. The local `tmp/` credential is excluded from the Docker build context. See [Weather forecasts](./integrations.md#weather-forecasts).

## Push worker operations

Production switched on 2026-10-04 through Cloud Build `eebc6834-f4d6-4748-af8f-2122608a94b3` and Terraform: Web revision `kakeratta-00223-pw9`, worker revision `kakeratta-worker-00001-vh8`. Both reported ready; worker resource settings matched Terraform. Pending legacy reactions were zero. Verification covered an authenticated Pub/Sub push, a Scheduler-triggered outbox repair returning 204, durable claiming and completion of an isolated orphan-routing reaction, and removal of its outbox and temporary workspace. No real model call or user entry was used. The public site and auth configuration endpoint returned 200, the scheduler was enabled without a last-attempt error, and existing Cloud Tasks alarms remained present. Initial push attempts returned 403 during IAM propagation before subsequent attempts succeeded.

Terraform enables `queue_worker_enabled` by default. Deploy the queue-aware image and apply Terraform with that image tag. This creates the private worker, Pub/Sub topic/subscription and IAM, the `dispatchOutbox.readyAt` index, and a minute Cloud Scheduler repair job. Wait for service revisions and the index to become ready, then confirm successful Pub/Sub delivery and persisted routing/delivery results. Set `queue_worker_enabled = false` to use the combined runtime instead. See [Terraform](../../infra/worker.tf).

No legacy queue migration is included for the current single-user deployment. Finish pending work on the existing combined service before switching. Existing entries, settings and persona memory keep their storage format. Any pending reaction without a dispatch generation will not be picked up by push workers; no queue data is deleted automatically.

Worker CPU, memory and instance limits are managed in `infra/worker.tf` and `infra/variables.tf`; increase Web and worker capacity independently as measured demand grows. It requires no waiting instance, but actual requests, Pub/Sub, Firestore and Scheduler can add charges. The minute repair request/query occurs even when idle. Web retains its existing CPU/memory configuration because other handlers can start background tasks. Cloud Build updates an existing worker to the same image before Web; Terraform creates its initial revision.

Monitor memory exits, eight-minute cancellations, redelivery, oldest unacknowledged message age, due outbox growth, and scheduler failures. Check resource sizing against real provider workflows. Unacknowledged messages are retained seven days; this initial configuration has no dead-letter subscription. After retention expires, inspect pending reactions and explicitly reissue dispatch requests: unpublished-only repair cannot recover expired published messages. Real-provider latency, long workflows and Firestore load need measurement before claiming large-user capacity. Split modes require `KAKERATTA_JOB_TOPIC`, `KAKERATTA_JOB_SUBSCRIPTION`, `KAKERATTA_WORKER_URL`, and `KAKERATTA_DISPATCH_URL` in addition to the existing Firestore, GCS and Cloud Tasks settings.

## Inspecting model usage

After deploying the usage instrumentation, read structured inference records from both Web and worker services:

```sh
gcloud logging read 'resource.type="cloud_run_revision" AND jsonPayload.path="model-usage"' --project=kakeratta --freshness=1d --limit=10000 --format=json
```

Fix the date range and timezone when reconciling provider billing, and check that the result limit was not reached. Group by `purpose`, `model`, and `wake` to distinguish post-triggered work from autonomous check-ins. Sum `usage.prompt_tokens` and `usage.completion_tokens` only when reported; count missing values separately. Count records for observed requests and use retry fields to identify correction, phase, note-job and reaction-job scopes. Reported reasoning/cache details are breakdowns, not extra counts to add to the totals. External agents and development traffic billed directly to the same provider account require separate reconciliation. The [backend logging specification](./backend.md#model-usage-logging) defines fields and failure semantics; records begin with the deployment and cannot reconstruct earlier usage.

## Deployment

The Dockerfile builds the Go binary and runs the API or the push worker according to `KAKERATTA_MODE`. The frontend is not embedded in the container. `cloudbuild.yaml` defines the image build and push, Cloud Run deployment, frontend build, and Firebase Hosting deployment as steps with explicit dependencies. The frontend copy and the firebase CLI install run beside the image build, and the hosting deployment waits for the Cloud Run deployment. The image is built on an `E2_HIGHCPU_8` worker, because a fresh worker has no Go build cache and the compile dominates the build; a registry layer cache was tried and cost more to pull and push than it saved.

`firebase.json` forwards `/api/**` to Cloud Run and serves `frontend/dist`. `infra` holds cloud resource and environment configuration. Cloud Run keeps no instance up (`min_instance_count = 0`) and may scale to `max_instances` (default 2); the Web CPU stays allocated while an instance is up for remaining background handlers such as exports. In combined mode persona turns also continue after the accepting request, with interrupted work recovered on startup or wake. In the production split mode, persona work runs within worker push requests and is recovered through broker retry and durable outbox successors. Any instance takes up any work (see [Multiple instances](./architecture.md#multiple-instances)). Times are not kept by the service: a Cloud Tasks queue (`kakeratta-due`) holds the wakes, and each task calls back `POST /internal/due` on the service's own `run.app` URL with an identity token of the app's account. `KAKERATTA_TASKS_QUEUE`, `KAKERATTA_TASKS_URL`, and `KAKERATTA_TASKS_ACCOUNT` set this up; without them, timers in each process do (see [Times and wakes](./architecture.md#times-and-wakes)). Firestore TTL policies on `expireAt` sweep leftover `events`, `asks`, and `trials` documents. This document explains repository implementation; it is not a procedure that executes a deployment.

## Web Push configuration

Generate a VAPID pair once with `go run ./cmd/webpush-keys` ([main.go](../../cmd/webpush-keys/main.go)). Store the private value securely, and configure the same pair on every API instance:

- `KAKERATTA_PUSH_PUBLIC_KEY`: base64url P-256 public key.
- `KAKERATTA_PUSH_PRIVATE_KEY`: base64url private key; keep this in the deployment's secret configuration.
- `KAKERATTA_PUSH_SUBJECT`: operator contact as `mailto:operator@example.com` or an HTTPS URL.

Without all three valid values, the push API reports unavailable and scheduled timeline posts continue normally. Terraform supplies the public key and contact from `push_public_key` and `push_subject`, defaulting to the generated key and `mailto:takeshy.work@gmail.com`. It maps `KAKERATTA_PUSH_PRIVATE_KEY` to the existing Secret Manager secret named by `push_private_key_secret` (default `kakeratta-vapid-private`) and version `push_private_key_version` (default `latest`). The operator creates the secret and its value; Terraform references it without retrieving the private key into state. The runtime service account already has `roles/secretmanager.secretAccessor`. Apply the Terraform service change before the next image-only Cloud Build deployment; that pipeline preserves the configured environment. Keep keys stable across releases; rotation requires users to disable and re-enable notifications. Build and deploy the frontend alongside the API so the updated `/sw.js` is served. Users enable notifications under **This device** in settings; browser permission alone does not register a device. HTTPS and a supported browser are required; iOS/iPadOS users open the app from the Home Screen.

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
- [cloudbuild.yaml](../../cloudbuild.yaml)
- [firebase.json](../../firebase.json)
- [main.tf](../../infra/main.tf): scaling, Cloud Tasks, VAPID secret references, permissions, and TTL policies
- [variables.tf](../../infra/variables.tf): Web Push public key, contact, secret name, and version
- [mise.toml](../../mise.toml)
- [README.md](../../infra/README.md)
- [migrate-memory](../../cmd/migrate-memory/main.go): one-off memory migration

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
