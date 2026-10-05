terraform {
  required_version = ">= 1.7"
  required_providers {
    google = { source = "hashicorp/google", version = ">= 6.0, < 8.0" }
  }
}

variable "project_id" { default = "geminihub-486523" }
variable "region" { default = "asia-northeast1" }
variable "zone" { default = "asia-northeast1-b" }
variable "machine_type" { default = "e2-medium" }
variable "bundle_object" { type = string }
variable "runtime_secret" { default = "shared-apps-runtime" }

provider "google" {
  project = var.project_id
  region  = var.region
  zone    = var.zone
}

locals {
  name = "shared-apps"
  builds = toset([
    "gemini-hub-build@geminihub-486523.iam.gserviceaccount.com",
    "523840037797-compute@developer.gserviceaccount.com",
    "geo-home-build@geo-home-mcp-505104.iam.gserviceaccount.com",
  ])
  runtimes = {
    gemihub   = "gemini-hub-run@geminihub-486523.iam.gserviceaccount.com"
    kakeratta = "kakeratta@kakeratta.iam.gserviceaccount.com"
    geo       = "geo-home-run@geo-home-mcp-505104.iam.gserviceaccount.com"
  }
}

resource "google_project_iam_member" "build_ssh" {
  for_each = { for p in setproduct(local.builds, ["roles/compute.osAdminLogin", "roles/iap.tunnelResourceAccessor"]) : "${p[0]}:${p[1]}" => p }
  project  = var.project_id
  role     = each.value[1]
  member   = "serviceAccount:${each.value[0]}"
}

resource "google_service_account_iam_member" "build_uses_vm" {
  for_each           = local.builds
  service_account_id = google_service_account.bootstrap.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${each.value}"
}

resource "google_storage_bucket_iam_member" "frontend_upload" {
  bucket = google_storage_bucket.config.name
  role   = "roles/storage.objectCreator"
  member = "serviceAccount:523840037797-compute@developer.gserviceaccount.com"
}

resource "google_service_account" "bootstrap" {
  account_id   = local.name
  display_name = "Shared VM bootstrap and scoped runtime impersonation"
}

resource "google_service_account_iam_member" "runtime" {
  for_each           = local.runtimes
  service_account_id = "projects/${split(".iam.", split("@", each.value)[1])[0]}/serviceAccounts/${each.value}"
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = "serviceAccount:${google_service_account.bootstrap.email}"
}

resource "google_project_iam_member" "registry" {
  for_each = toset([var.project_id, "kakeratta", "geo-home-mcp-505104"])
  project  = each.value
  role     = "roles/artifactregistry.reader"
  member   = "serviceAccount:${google_service_account.bootstrap.email}"
}

resource "google_project_iam_member" "logging" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.bootstrap.email}"
}

resource "google_project_iam_member" "monitoring" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.bootstrap.email}"
}

resource "google_cloud_tasks_queue" "due" {
  project  = "kakeratta"
  name     = "kakeratta-vm-due"
  location = "asia-east1"
  rate_limits { max_dispatches_per_second = 50 }
  retry_config {
    max_attempts  = 5
    min_backoff   = "10s"
    max_backoff   = "300s"
    max_doublings = 4
  }
}

locals {
  certificates = {
    kakeratta = { domain = "kakeratta.net", domains = ["kakeratta.net", "*.kakeratta.net"], project = "kakeratta", zone = "kakeratta" }
    geo       = { domain = "geo.mcp.takeshy.work", domains = ["geo.mcp.takeshy.work"], project = "takeshy-work", zone = "takeshy-work" }
  }
}

resource "google_certificate_manager_dns_authorization" "apps" {
  for_each = local.certificates
  name     = "shared-apps-${each.key}"
  domain   = each.value.domain
  type     = "PER_PROJECT_RECORD"
}

resource "google_dns_record_set" "validation" {
  for_each     = local.certificates
  project      = each.value.project
  managed_zone = each.value.zone
  name         = google_certificate_manager_dns_authorization.apps[each.key].dns_resource_record[0].name
  type         = "CNAME"
  ttl          = 300
  rrdatas      = [google_certificate_manager_dns_authorization.apps[each.key].dns_resource_record[0].data]
}

resource "google_certificate_manager_certificate" "apps" {
  for_each = local.certificates
  name     = "shared-apps-${each.key}"
  managed {
    domains            = each.value.domains
    dns_authorizations = [google_certificate_manager_dns_authorization.apps[each.key].id]
  }
  depends_on = [google_dns_record_set.validation]
}

resource "google_certificate_manager_certificate_map_entry" "apps" {
  for_each     = { kakeratta = "kakeratta.net", kakeratta_wildcard = "*.kakeratta.net", geo = "geo.mcp.takeshy.work" }
  name         = "shared-apps-${replace(each.key, "_", "-")}"
  map          = "gemihub-map"
  hostname     = each.value
  certificates = [google_certificate_manager_certificate.apps[startswith(each.key, "kakeratta") ? "kakeratta" : "geo"].id]
}

resource "google_storage_bucket" "config" {
  name                        = "${var.project_id}-shared-apps"
  location                    = upper(var.region)
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  lifecycle { prevent_destroy = true }
}

resource "google_storage_bucket_iam_member" "config" {
  bucket = google_storage_bucket.config.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.bootstrap.email}"
}

resource "google_secret_manager_secret_iam_member" "config" {
  project   = var.project_id
  secret_id = var.runtime_secret
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.bootstrap.email}"
}

resource "google_compute_firewall" "lb" {
  name          = "shared-apps-load-balancer"
  network       = "default"
  priority      = 800
  source_ranges = ["35.191.0.0/16", "130.211.0.0/22"]
  target_tags   = [local.name]
  allow {
    protocol = "tcp"
    ports    = ["8080", "8085"]
  }
}

resource "google_compute_firewall" "iap" {
  name          = "shared-apps-iap-ssh"
  network       = "default"
  priority      = 800
  source_ranges = ["35.235.240.0/20"]
  target_tags   = [local.name]
  allow {
    protocol = "tcp"
    ports    = ["22"]
  }
}

resource "google_compute_firewall" "deny_other_ingress" {
  name          = "shared-apps-deny-other-ingress"
  network       = "default"
  priority      = 900
  source_ranges = ["0.0.0.0/0"]
  target_tags   = [local.name]
  deny { protocol = "all" }
}

resource "google_compute_instance" "apps" {
  name                      = local.name
  machine_type              = var.machine_type
  allow_stopping_for_update = true
  deletion_protection       = true
  tags                      = [local.name]
  boot_disk {
    initialize_params {
      image = "debian-cloud/debian-12"
      size  = 30
      type  = "pd-balanced"
    }
  }
  network_interface {
    network = "default"
    access_config {}
  }
  service_account {
    email  = google_service_account.bootstrap.email
    scopes = ["cloud-platform"]
  }
  metadata = {
    enable-oslogin         = "TRUE"
    block-project-ssh-keys = "TRUE"
    shared-bundle          = "gs://${google_storage_bucket.config.name}/${var.bundle_object}"
    shared-runtime-secret  = "projects/${var.project_id}/secrets/${var.runtime_secret}/versions/latest"
    startup-script         = file("${path.module}/bootstrap.sh")
  }
  depends_on = [google_service_account_iam_member.runtime, google_project_iam_member.registry, google_secret_manager_secret_iam_member.config, google_storage_bucket_iam_member.config]
}

resource "google_compute_instance_group" "apps" {
  name      = local.name
  instances = [google_compute_instance.apps.self_link]
  named_port {
    name = "http"
    port = 8080
  }
}

resource "google_compute_health_check" "apps" {
  for_each    = toset(["gemihub", "kakeratta", "geo"])
  name        = "shared-apps-${each.value}"
  timeout_sec = 5
  http_health_check {
    port         = 8085
    request_path = "/health/${each.value}"
  }
}

resource "google_compute_backend_service" "apps" {
  for_each              = toset(["gemihub", "kakeratta", "geo"])
  name                  = "shared-apps-${each.value}"
  protocol              = "HTTP"
  port_name             = "http"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  timeout_sec           = 600
  health_checks         = [google_compute_health_check.apps[each.value].id]
  enable_cdn            = each.value == "gemihub"
  dynamic "cdn_policy" {
    for_each = each.value == "gemihub" ? [1] : []
    content {
      cache_mode                   = "USE_ORIGIN_HEADERS"
      signed_url_cache_max_age_sec = 0
    }
  }
  backend {
    group           = google_compute_instance_group.apps.id
    balancing_mode  = "UTILIZATION"
    max_utilization = 0.8
  }
  connection_draining_timeout_sec = 300
}

output "instance" { value = google_compute_instance.apps.name }
output "config_bucket" { value = google_storage_bucket.config.name }
output "backends" { value = { for k, v in google_compute_backend_service.apps : k => v.self_link } }
