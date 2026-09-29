---
type: "Process"
title: "Development and workspace operations"
description: "Development, validation, deployment, portable exports, and workspace deletion."
tags: ["development", "deployment", "export", "destroy"]
updated: "2026-09-30"
---

Development, validation, deployment, portable exports, and workspace deletion.

# Development, validation, and deployment

## Development units

`Taskfile.yml` groups API build and startup, Vite, Storybook, frontend builds, and tests. The default API address is `127.0.0.1:8787`. Vite serves the frontend and proxies development `/api` requests to the API.

`frontend/build.mjs` creates distribution files by copying into `frontend/dist`. The browser loads ES modules directly; the application is not bundled. The build also assembles public assets, the landing page, manual, screenshots, and the Service Worker cache manifest.

## Validation

```sh
go test ./...
node --test --test-isolation=none tests/*.test.js
node frontend/build.mjs
```

Go tests cover persistence, HTTP, personas, search, and integrations. Frontend tests exercise state transitions and DOM operations under Node. Storybook provides screen examples, and `offline-check.mjs` checks offline behavior in a browser. Real model providers, OAuth, microphones, and similar integrations require checks beyond unit tests.

## Deployment

The Dockerfile builds the Go binary and runs only the API. The frontend is not embedded in the container. `cloudbuild.yaml` defines image build and push, Cloud Run deployment, frontend build, and Firebase Hosting deployment in sequence.

`firebase.json` forwards `/api/**` to Cloud Run and serves `frontend/dist`. `infra` holds cloud resource and environment configuration. This document explains repository implementation; it is not a procedure that executes a deployment.

## Implementation and verification

- [Taskfile.yml](../../Taskfile.yml)
- [vite.config.js](../../frontend/vite.config.js)
- [build.mjs](../../frontend/build.mjs)
- [Dockerfile](../../Dockerfile)
- [cloudbuild.yaml](../../cloudbuild.yaml)
- [firebase.json](../../firebase.json)
- [README.md](../../infra/README.md)

# Workspace export and deletion

## Backup format

`Workspace.Export` converts state into the layout a local kakeratta reads and passes files to a sink in sequence. The HTTP layer streams a zip without assembling the entire workspace in memory. It includes posts, personas, memory, images, and related data.

Export settings omit API credentials, and Bluesky tokens and MCP connection keys are excluded as well. Connections must be configured again after restoration. The implementation does not simply zip the entire workspace directory.

## Delivery paths

With an external ExportTarget, `POST /api/export` starts asynchronous work. Completion sends the download location over SSE. The GCS target creates an expiring URL. Without an export target, `GET /api/export` streams the download directly. Concurrent exports are limited, and one export has a 30-minute deadline.

## Deletion

For hosted workspace deletion, Gateway accepts a confirmed request, takes the user out of service, closes Runner and related workers, then calls `Destroy` and removes backups. It subsequently removes the login and cookie, and the frontend clears device storage. A standalone local Server returns 409 for deletion and tells the user to stop the app and remove the workspace directory manually.

## Implementation and verification

- [export.go](../../internal/server/export.go)
- [export.go](../../internal/store/export.go)
- [exports.go](../../internal/store/exports.go)
- [destroy.go](../../internal/server/destroy.go)
- [destroy.go](../../internal/store/destroy.go)
- [export_test.go](../../internal/server/export_test.go)
- [destroy_test.go](../../internal/store/destroy_test.go)

# Related chapters

- [Architecture and runtime](./architecture.md)
- [API and persistent state](./backend.md)
- [Browser application and device state](./frontend.md)

Local timeline files and Firestore exports use `timeline/YYYY/MM/DD.json`: one JSON array of current entries per date. Firestore export streams entries in timestamp order and buffers one day at a time. Restoring the exported directory rebuilds the local timeline index on startup.
