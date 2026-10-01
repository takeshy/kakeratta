---
type: "Process"
title: "Development and workspace operations"
description: "Development, validation, deployment, memory migration, portable exports, and workspace deletion."
tags: ["development", "deployment", "export", "destroy"]
updated: "2026-10-01"
---

Development, validation, deployment, memory migration, portable exports, and workspace deletion.

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

Go tests cover persistence, HTTP, personas, search, and integrations. Firestore tests, including the coordination and two-instance tests, run against the Firestore emulator when `FIRESTORE_EMULATOR_HOST` is set (for example `firebase emulators:start --only firestore`) and are skipped otherwise. `mise.toml` pins Go 1.26 and Node 24, matching the Dockerfile and Cloud Build. Frontend tests exercise state transitions and DOM operations under Node. Storybook provides screen examples, and `offline-check.mjs` checks offline behavior in a browser. Real model providers, OAuth, microphones, and similar integrations require checks beyond unit tests.

WeatherAPI unit tests use a local mock provider. The opt-in live check consumes one provider request and loads the operator key without printing it:

```sh
KAKERATTA_WEATHERAPI_KEY_FILE="$PWD/tmp/weatherapi.key" KAKERATTA_WEATHERAPI_LIVE_TEST=1 go test ./internal/tools -run '^TestWeatherForecastLive$' -v
```

Production deployment binds `KAKERATTA_WEATHERAPI_KEY` to the existing Secret Manager secret `kakeratta-wheatherapi-key`, version `latest`, in both the Terraform Cloud Run definition (`weatherapi_secret_id` / `weatherapi_secret_version`) and Cloud Build deployment (`_WEATHERAPI_SECRET` / `_WEATHERAPI_SECRET_VERSION`). The spelling is the registered secret ID. The runtime service account already has Secret Manager accessor permission. The operator manages secret versions separately; no WeatherAPI secret payload is read or created by Terraform, and only the secret reference enters state. A new instance resolves the configured version on startup. Keep both deployment paths aligned when overriding the secret ID or version. The local `tmp/` credential is excluded from the Docker build context. See [Weather forecasts](./integrations.md#weather-forecasts).

## Deployment

The Dockerfile builds the Go binary and runs only the API. The frontend is not embedded in the container. `cloudbuild.yaml` defines the image build and push, Cloud Run deployment, frontend build, and Firebase Hosting deployment as steps with explicit dependencies. The frontend copy and the firebase CLI install run beside the image build, and the hosting deployment waits for the Cloud Run deployment. The image is built on an `E2_HIGHCPU_8` worker, because a fresh worker has no Go build cache and the compile dominates the build; a registry layer cache was tried and cost more to pull and push than it saved.

`firebase.json` forwards `/api/**` to Cloud Run and serves `frontend/dist`. `infra` holds cloud resource and environment configuration. Cloud Run keeps no instance up (`min_instance_count = 0`) and may scale to `max_instances` (default 2); the CPU stays allocated while an instance is up, since a turn runs on after its request has answered, and one cut short by an instance going away is found again at the next start or wake. Any instance takes up any work (see [Multiple instances](./architecture.md#multiple-instances)). Times are not kept by the service: a Cloud Tasks queue (`kakeratta-due`) holds the wakes, and each task calls back `POST /internal/due` on the service's own `run.app` URL with an identity token of the app's account. `KAKERATTA_TASKS_QUEUE`, `KAKERATTA_TASKS_URL`, and `KAKERATTA_TASKS_ACCOUNT` set this up; without them, timers in each process do (see [Times and wakes](./architecture.md#times-and-wakes)). Firestore TTL policies on `expireAt` sweep leftover `events`, `asks`, and `trials` documents. This document explains repository implementation; it is not a procedure that executes a deployment.

## Web Push configuration

Generate a VAPID pair once with `go run ./cmd/webpush-keys` ([main.go](../../cmd/webpush-keys/main.go)). Store the private value securely, and configure the same pair on every API instance:

- `KAKERATTA_PUSH_PUBLIC_KEY`: base64url P-256 public key.
- `KAKERATTA_PUSH_PRIVATE_KEY`: base64url private key; keep this in the deployment's secret configuration.
- `KAKERATTA_PUSH_SUBJECT`: operator contact as `mailto:operator@example.com` or an HTTPS URL.

Without all three valid values, the push API reports unavailable and scheduled timeline posts continue normally. Terraform supplies the public key and contact from `push_public_key` and `push_subject`, defaulting to the generated key and `mailto:takeshy.work@gmail.com`. It maps `KAKERATTA_PUSH_PRIVATE_KEY` to the existing Secret Manager secret named by `push_private_key_secret` (default `kakeratta-vapid-private`) and version `push_private_key_version` (default `latest`). The operator creates the secret and its value; Terraform references it without retrieving the private key into state. The runtime service account already has `roles/secretmanager.secretAccessor`. Apply the Terraform service change before the next image-only Cloud Build deployment; that pipeline preserves the configured environment. Keep keys stable across releases; rotation requires users to disable and re-enable notifications. Build and deploy the frontend alongside the API so the updated `/sw.js` is served. Users enable notifications under **This device** in settings; browser permission alone does not register a device. HTTPS and a supported browser are required; iOS/iPadOS users open the app from the Home Screen.

Web Push uses [webpush-go](https://github.com/SherClockHolmes/webpush-go) for payload encryption and VAPID signing. Unit tests stub providers; actual device delivery, browser/OS permissions, and background behavior require a configured deployment and a real-device check. Delivery is best effort without automatic server retries; identity verification also suppresses notifications when login has expired or the API cannot be reached.

## Memory migration

Persona memory moved from one note per thing to field summaries and kakera records, and the running app no longer reads the old notes. Fold them once after deploying that change with [cmd/migrate-memory](../../cmd/migrate-memory/main.go), which runs `Runner.MigrateMemory` for every persona of a workspace on that workspace's own model settings:

- `go run ./cmd/migrate-memory` - the local workspace (`KAKERATTA_HOME` or the default); `-firestore kakeratta` - every workspace in production Firestore with application default credentials, or one with `-user KEY`; `-persona ID` limits it to one persona.
- Without `-apply` it prints, per persona, how many old notes there are, which were left out for length, and the summaries the model would write, and saves them to `-plan` (JSON, default `migration-plan.json`); nothing is changed. The plan may be edited by hand.
- With `-apply` it writes the summaries in `-plan` as they are, without calling the model again (`Runner.ApplyMigration`), and deletes the old notes. A persona whose old notes changed since the dry run, or whose plan holds an unknown or over-long field, is refused. Running the dry run again afterwards finds nothing to do.
- The production migration of 2026-10-01 was done this way: three dry runs while tightening the prompt (no researched figures or explanations; the user's actions kept with their dates; the notes' own language), a hand edit of the plan, then `-apply`.
- Between the deploy and the run, personas answer without their old memory.

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

`Workspace.Export` converts state into the layout a local kakeratta reads and passes files to a sink in sequence. The HTTP layer streams a zip without assembling the entire workspace in memory. It includes posts, personas, memory, images, and related data.

Local timeline files and Firestore exports use `timeline/YYYY/MM/DD.json`: one JSON array of current entries per date. Firestore export streams entries in timestamp order and buffers one day at a time. Restoring the exported directory rebuilds the local timeline index on startup. Unfinished reactions, runtime records, locks, and events are runtime state and are not exported.

Export settings omit API credentials, and Bluesky tokens and MCP connection keys are excluded as well. Connections must be configured again after restoration. The implementation does not simply zip the entire workspace directory.

## Delivery paths

With an external ExportTarget, `POST /api/export` starts asynchronous work. Completion sends the download location over SSE. The GCS target creates an expiring URL. Without an export target, `GET /api/export` streams the download directly. Concurrent exports are limited, and one export has a 30-minute deadline.

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

