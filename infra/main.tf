locals {
  name            = "kakeratta"
  repository      = "kakeratta"
  image           = "${var.region}-docker.pkg.dev/${var.project_id}/${local.repository}/${local.name}:${var.image_tag}"
  domain          = trimsuffix(var.domain, ".")
  dns_name        = "${local.domain}."
  fallback_domain = "${replace(google_compute_address.app.address, ".", "-")}.sslip.io"
  access_token    = coalesce(var.access_token, random_password.access_token.result)
  services = toset([
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "compute.googleapis.com",
    "dns.googleapis.com",
    "firestore.googleapis.com",
    "firebase.googleapis.com",
    "firebasehosting.googleapis.com",
    "iam.googleapis.com",
    "identitytoolkit.googleapis.com",
    "secretmanager.googleapis.com",
    "storage.googleapis.com",
    "run.googleapis.com",
  ])
}

resource "google_dns_managed_zone" "app" {
  name        = local.name
  dns_name    = local.dns_name
  description = "Public DNS zone for kakeratta"

  depends_on = [google_project_service.required]
}

resource "google_dns_record_set" "apex" {
  name         = google_dns_managed_zone.app.dns_name
  managed_zone = google_dns_managed_zone.app.name
  type         = "A"
  ttl          = 300
  rrdatas      = ["199.36.158.100"]
}

resource "google_dns_record_set" "firebase_hosting_verification" {
  name         = google_dns_managed_zone.app.dns_name
  managed_zone = google_dns_managed_zone.app.name
  type         = "TXT"
  ttl          = 300
  rrdatas      = ["\"hosting-site=kakeratta\""]
}

resource "google_dns_record_set" "www" {
  name         = "www.${google_dns_managed_zone.app.dns_name}"
  managed_zone = google_dns_managed_zone.app.name
  type         = "CNAME"
  ttl          = 300
  rrdatas      = ["kakeratta.web.app."]
}

data "google_project" "current" {
  project_id = var.project_id
}

resource "google_project_service" "required" {
  for_each = local.services

  project            = var.project_id
  service            = each.value
  disable_on_destroy = false
}

resource "google_artifact_registry_repository" "app" {
  location      = var.region
  repository_id = local.repository
  description   = "Container images for kakeratta"
  format        = "DOCKER"

  depends_on = [google_project_service.required]
}

resource "google_compute_address" "app" {
  name   = "${local.name}-ipv4"
  region = var.region

  depends_on = [google_project_service.required]
}

resource "google_storage_bucket" "blobs" {
  name                        = "${var.project_id}-${local.name}-blobs"
  location                    = var.run_region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.required]
}

resource "google_firestore_database" "app" {
  project                     = var.project_id
  name                        = "(default)"
  location_id                 = var.run_region
  type                        = "FIRESTORE_NATIVE"
  delete_protection_state     = "DELETE_PROTECTION_ENABLED"
  deletion_policy             = "ABANDON"
  app_engine_integration_mode = "DISABLED"

  depends_on = [google_project_service.required]
}

resource "random_password" "access_token" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "access_token" {
  secret_id = "${local.name}-access-token"

  replication {
    auto {}
  }

  depends_on = [google_project_service.required]
}

resource "google_secret_manager_secret_version" "access_token" {
  secret      = google_secret_manager_secret.access_token.id
  secret_data = local.access_token
}

resource "google_service_account" "app" {
  account_id   = local.name
  display_name = "kakeratta runtime"

  depends_on = [google_project_service.required]
}

resource "google_project_iam_member" "app_roles" {
  for_each = toset([
    "roles/artifactregistry.reader",
    "roles/firebaseauth.admin",
    "roles/datastore.user",
    "roles/logging.logWriter",
    "roles/monitoring.metricWriter",
    "roles/secretmanager.secretAccessor",
  ])

  project = var.project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.app.email}"
}

resource "google_storage_bucket_iam_member" "app_blobs" {
  bucket = google_storage_bucket.blobs.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.app.email}"
}

resource "google_project_iam_member" "cloud_build" {
  project = var.project_id
  role    = "roles/cloudbuild.builds.builder"
  member  = "serviceAccount:${data.google_project.current.number}-compute@developer.gserviceaccount.com"

  depends_on = [google_project_service.required]
}

resource "google_cloud_run_v2_service" "app" {
  name                = local.name
  location            = var.run_region
  deletion_protection = true

  template {
    service_account = google_service_account.app.email

    scaling {
      min_instance_count = 1
      max_instance_count = 1
    }

    containers {
      image = local.image

      resources {
        cpu_idle = false
        limits = {
          cpu    = "1"
          memory = "1Gi"
        }
      }

      env {
        name  = "KAKERATTA_ADDR"
        value = ":8080"
      }
      env {
        name  = "KAKERATTA_HOME"
        value = "/tmp/kakeratta"
      }
      env {
        name  = "KAKERATTA_STORE"
        value = "firestore"
      }
      env {
        name  = "KAKERATTA_FIRESTORE_PROJECT_ID"
        value = var.project_id
      }
      env {
        name  = "KAKERATTA_FIREBASE_PROJECT_ID"
        value = var.firebase_project_id
      }
      env {
        name  = "KAKERATTA_FIREBASE_API_KEY"
        value = var.firebase_api_key
      }
      env {
        name  = "KAKERATTA_FIREBASE_AUTH_DOMAIN"
        value = var.firebase_auth_domain
      }
      env {
        name  = "KAKERATTA_GCS_BUCKET"
        value = google_storage_bucket.blobs.name
      }
      # MCP servers are the operator's, shared by every user. Each user's web
      # search key stays in their own settings.
      env {
        name  = "KAKERATTA_MCP_SERVERS"
        value = jsonencode(var.mcp_servers)
      }

      ports {
        container_port = 8080
      }
    }
  }

  depends_on = [
    google_firestore_database.app,
    google_project_iam_member.app_roles,
    google_storage_bucket_iam_member.app_blobs,
  ]
}

resource "google_cloud_run_v2_service_iam_member" "public" {
  project  = var.project_id
  location = google_cloud_run_v2_service.app.location
  name     = google_cloud_run_v2_service.app.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_compute_firewall" "web" {
  name    = "${local.name}-web"
  network = "default"

  allow {
    protocol = "tcp"
    ports    = ["80", "443"]
  }

  source_ranges = ["0.0.0.0/0"]
  target_tags   = [local.name]

  depends_on = [google_project_service.required]
}
