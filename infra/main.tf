locals {
  name         = "kakeratta"
  repository   = "kakeratta"
  image        = "${var.region}-docker.pkg.dev/${var.project_id}/${local.repository}/${local.name}:${var.image_tag}"
  domain       = "${replace(google_compute_address.app.address, ".", "-")}.sslip.io"
  access_token = coalesce(var.access_token, random_password.access_token.result)
  services = toset([
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "compute.googleapis.com",
    "iam.googleapis.com",
    "secretmanager.googleapis.com",
  ])
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

resource "google_compute_disk" "data" {
  name = "${local.name}-data"
  type = "pd-balanced"
  zone = var.zone
  size = var.data_disk_size_gb

  lifecycle {
    prevent_destroy = true
  }

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
    "roles/logging.logWriter",
    "roles/monitoring.metricWriter",
    "roles/secretmanager.secretAccessor",
  ])

  project = var.project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.app.email}"
}

resource "google_project_iam_member" "cloud_build" {
  project = var.project_id
  role    = "roles/cloudbuild.builds.builder"
  member  = "serviceAccount:${data.google_project.current.number}-compute@developer.gserviceaccount.com"

  depends_on = [google_project_service.required]
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

resource "google_compute_instance" "app" {
  name         = local.name
  machine_type = var.machine_type
  zone         = var.zone
  tags         = [local.name]

  boot_disk {
    initialize_params {
      image = "projects/cos-cloud/global/images/family/cos-stable"
      size  = 10
      type  = "pd-balanced"
    }
  }

  attached_disk {
    source      = google_compute_disk.data.id
    device_name = "${local.name}-data"
  }

  network_interface {
    network = "default"
    access_config {
      nat_ip = google_compute_address.app.address
    }
  }

  service_account {
    email  = google_service_account.app.email
    scopes = ["cloud-platform"]
  }

  metadata = {
    deployment-version = var.image_tag
    startup-script = templatefile("${path.module}/startup.sh.tftpl", {
      domain     = local.domain
      image      = local.image
      project_id = var.project_id
      secret_id  = google_secret_manager_secret.access_token.secret_id
    })
  }

  shielded_instance_config {
    enable_secure_boot          = true
    enable_vtpm                 = true
    enable_integrity_monitoring = true
  }

  depends_on = [
    google_artifact_registry_repository.app,
    google_project_iam_member.app_roles,
    google_secret_manager_secret_version.access_token,
  ]
}
