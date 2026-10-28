# Shared application VM

Cloud Run remains the preferred hosted configuration. This shared VM is a temporary arrangement to reduce the cost of keeping production unchanged through the roughly one-month judging wait after the 2026-10-31 submission deadline.

Production runs GemiHub, Kakeratta web/worker, and Geo MCP in separate Docker containers on `shared-apps` (`geminihub-486523`, `asia-northeast1-b`, `e2-medium`, 4 GiB). The existing global HTTPS load balancer, IP and certificate map remain in use. Nginx selects applications by Host; GemiHub remains the default for its custom domains. Firestore, GCS, OAuth/session/VAPID secrets, Pub/Sub stay in their original projects.

`main.tf` owns only the new VM, private configuration bucket, bootstrap account, scoped IAM, instance group, backend services, health checks, certificate authorization records and the `kakeratta-vm-due` queue. GemiHub's existing Terraform module continues to own the URL map and front end. Its `shared_vm_backends` setting selects these backends. Kakeratta's original module uses `shared_vm_ip` to retain the migrated DNS and Pub/Sub/Scheduler endpoints. Inspect both modules' plans before applying: the retained Cloud Run services are fallback resources, not the production deployment target.

State uses the private GCS backend in `takeshy-work-kakeratta-terraform`, prefix `state/shared-vm`. Use [the Terraform wrapper](../README.md#terraform-state-and-variables) to load the versioned private tfvars from GCS for plan/apply.

## Runtime credentials

Each application keeps its original runtime identity. The host broker maps each isolated Docker bridge to that identity and mints short-lived tokens through IAM Credentials. Containers receive a metadata-host override and cannot access the VM bootstrap account through the real metadata HTTP(S) endpoint. VM DNS remains available. The host resolves Secret Manager references with the application's own identity; secret values are passed through the deployment process environment, never CLI arguments. Docker configuration and `/opt/shared-apps/runtime.json` are accessible only to the operator/root. No service-account key is created.

The initial runtime manifest is stored in Secret Manager as `shared-apps-runtime`, separate from Terraform state. It contains existing literal configuration and secret references. The bootstrap tar archive contains scripts and static frontend files, not the manifest. The configuration bucket is private. Initial provisioning requires creating the secret/version, creating the bucket, uploading the bundle, then applying the full plan with `bundle_object` set to that immutable object.

## Deployment and recovery

The repository Cloud Build files build immutable images and deploy over IAP/OS Login:

```
sudo python3 /opt/shared-apps/deploy.py --app gemihub --image IMAGE
sudo python3 /opt/shared-apps/deploy.py --app kakeratta --image IMAGE --frontend gs://BUCKET/frontend/BUILD_ID.tar.gz
sudo python3 /opt/shared-apps/deploy.py --app geo --image IMAGE
```

Kakeratta's command drains/restarts both web and worker and swaps a validated static frontend archive. Deployments are serialized. Failed container startup restores the previous image. The current image selection is saved on the persistent boot disk; reboot keeps it. Before deliberately replacing that disk or restoring a VM from scratch, back up its current manifest to a new Secret Manager version and refresh the bootstrap frontend bundle. The initial secret/bundle alone represents the migration's initial versions. Do not print or commit a runtime manifest, Docker inspect environment or secret payload.

Kakeratta's `kakeratta-main-deploy` trigger reads the repository's `cloudbuild.yaml` as of 2026-10-28. This includes removing the build identity's old OS Login SSH keys before creating a deployment key, preventing the 32 KiB login-profile limit from blocking deployment. GemiHub retains its separate build configuration. Geo's `scripts/deploy.sh` uses `cloudbuild-vm.yaml`; its weekly map update remains paused. Geo runs with `GEO_ROUTES_ENABLED=false` and no OSRM endpoints.

Kakeratta builds run as `523840037797-compute@developer.gserviceaccount.com`. On the private configuration bucket, `frontend_upload` grants object creation and `frontend_upload_read` allows bucket listing plus object viewing only for the `frontend/` object prefix. `gcloud storage cp` checks the destination object and lists the bucket before uploading; creation alone fails with 403. The read condition excludes bootstrap bundle contents. Archives use unique build IDs; these bindings grant no object deletion or overwrite permission. The VM bootstrap account separately retains bucket-wide object viewing for bundle and frontend downloads.

The old timer queue remains paused as a rollback snapshot. Its full scheduled HTTP requests were copied into the new queue with updated URL and token audience before resuming. New Pub/Sub pushes and Scheduler repair use `https://kakeratta.net/internal/work` and `/internal/dispatch`; alarms use `/internal/due`. The existing service account authenticates all three. Geo MCP uses the same X-API-Key directly at `https://geo.mcp.takeshy.work/mcp`; API Gateway and OSRM servers are removed. For rollback, first pause the new queue and schedulers, restore endpoints and DNS/URL-map settings, reconcile alarms created since migration, then resume the chosen queue. Do not run both timer queues concurrently.

## Verification and sizing

Health checks on port 8085 require the relevant container ports to be available; they do not verify Firestore or model providers. Check application requests and authenticated internal pushes separately. The VM firewall accepts app/health traffic only from Google load-balancer ranges and SSH only through IAP. Verify effective VPC firewall rules when adding instances. Containers have explicit memory limits and no swap; watch OOM/restarts and provider workloads.

Ops Agent exports VM memory/CPU metrics and parses Docker logs into structured Cloud Logging records in `geminihub-486523`, with `labels.service_name` identifying the container. The receiver name preserves the BigQuery sink table name `run_googleapis_com_stdout`. Inspect model usage with:

```
gcloud logging read 'resource.type="gce_instance" AND labels.service_name:"kakeratta" AND jsonPayload.path="model-usage"' --project=geminihub-486523 --freshness=1d --limit=10000 --format=json
```

Resize with `machine_type` (e.g. `e2-small` for 2 GiB) and an inspected Terraform plan. Changing VM machine type stops/restarts this single VM and interrupts all three services. Reduce container memory limits first if moving to 2 GiB. There is no automatic failover to a second VM.

Local checks:

```
python3 -m unittest discover -s infra/shared-vm -p 'test_*.py'
python3 -m py_compile infra/shared-vm/control.py infra/shared-vm/deploy.py
bash -n infra/shared-vm/bootstrap.sh
terraform -chdir=infra/shared-vm fmt -check
terraform -chdir=infra/shared-vm validate
```
