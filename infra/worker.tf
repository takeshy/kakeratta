locals {
  worker_url   = "https://${local.name}-worker-${data.google_project.current.number}.${var.run_region}.run.app/internal/work"
  dispatch_url = "https://${local.name}-${data.google_project.current.number}.${var.run_region}.run.app/internal/dispatch"
  queue_env = {
    KAKERATTA_JOB_TOPIC        = "projects/${var.project_id}/topics/${local.name}-jobs"
    KAKERATTA_JOB_SUBSCRIPTION = "projects/${var.project_id}/subscriptions/${local.name}-jobs"
    KAKERATTA_WORKER_URL       = local.worker_url
    KAKERATTA_DISPATCH_URL     = local.dispatch_url
  }
}
resource "google_pubsub_topic" "jobs" {
  count      = var.queue_worker_enabled ? 1 : 0
  name       = "${local.name}-jobs"
  depends_on = [google_project_service.required]
}
resource "google_pubsub_topic_iam_member" "publisher" {
  count  = var.queue_worker_enabled ? 1 : 0
  topic  = google_pubsub_topic.jobs[0].name
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:${google_service_account.app.email}"
}
resource "google_project_service_identity" "pubsub" {
  provider   = google-beta
  count      = var.queue_worker_enabled ? 1 : 0
  project    = var.project_id
  service    = "pubsub.googleapis.com"
  depends_on = [google_project_service.required]
}
resource "google_service_account_iam_member" "pubsub_tokens" {
  count              = var.queue_worker_enabled ? 1 : 0
  service_account_id = google_service_account.app.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = "serviceAccount:${google_project_service_identity.pubsub[0].email}"
  depends_on         = [google_project_service.required]
}
resource "google_cloud_run_v2_service_iam_member" "worker_invoker" {
  count    = var.queue_worker_enabled ? 1 : 0
  project  = var.project_id
  location = var.run_region
  name     = google_cloud_run_v2_service.worker[0].name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.app.email}"
}
resource "google_pubsub_subscription" "jobs" {
  count                      = var.queue_worker_enabled ? 1 : 0
  name                       = "${local.name}-jobs"
  topic                      = google_pubsub_topic.jobs[0].id
  ack_deadline_seconds       = 600
  message_retention_duration = "604800s"
  expiration_policy { ttl = "" }
  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }
  push_config {
    push_endpoint = local.worker_url
    oidc_token {
      service_account_email = google_service_account.app.email
      audience              = local.worker_url
    }
  }
  depends_on = [google_service_account_iam_member.pubsub_tokens, google_cloud_run_v2_service_iam_member.worker_invoker]
}
resource "google_firestore_field" "dispatch_ready" {
  count      = var.queue_worker_enabled ? 1 : 0
  project    = var.project_id
  database   = google_firestore_database.app.name
  collection = "dispatchOutbox"
  field      = "readyAt"
  index_config {
    indexes {
      order       = "ASCENDING"
      query_scope = "COLLECTION_GROUP"
    }
    indexes {
      order       = "ASCENDING"
      query_scope = "COLLECTION"
    }
  }
}
resource "google_cloud_scheduler_job" "dispatch" {
  count            = var.queue_worker_enabled ? 1 : 0
  name             = "${local.name}-dispatch"
  region           = var.run_region
  schedule         = "* * * * *"
  attempt_deadline = "60s"
  http_target {
    uri         = local.dispatch_url
    http_method = "POST"
    oidc_token {
      service_account_email = google_service_account.app.email
      audience              = local.dispatch_url
    }
  }
  depends_on = [google_project_service.required, google_firestore_field.dispatch_ready, google_cloud_run_v2_service.app]
}

resource "google_cloud_run_v2_service" "worker" {
  count               = var.queue_worker_enabled ? 1 : 0
  name                = "${local.name}-worker"
  location            = var.run_region
  deletion_protection = true
  # Pub/Sub uses the endpoint URL as audience; allow it at Cloud Run IAM too.
  custom_audiences = [local.worker_url]
  template {
    service_account                  = google_service_account.app.email
    execution_environment            = "EXECUTION_ENVIRONMENT_GEN1"
    max_instance_request_concurrency = 1
    timeout                          = "540s"
    scaling {
      min_instance_count = 0
      max_instance_count = var.worker_max_instances
    }
    containers {
      image = local.image
      env {
        name  = "GOMEMLIMIT"
        value = "96MiB"
      }
      env {
        name  = "GOMAXPROCS"
        value = "1"
      }
      resources {
        cpu_idle = true
        limits = {
          cpu    = "0.08"
          memory = "128Mi"
        }
      }
      env {
        name  = "KAKERATTA_MODE"
        value = "worker"
      }
      dynamic "env" {
        for_each = local.queue_env
        content {
          name  = env.key
          value = env.value
        }
      }
      env {
        name  = "KAKERATTA_ADDR"
        value = ":8080"
      }
      env {
        name  = "KAKERATTA_PUBLIC_URL"
        value = var.bluesky_public_url != "" ? var.bluesky_public_url : "https://${local.domain}"
      }
      env {
        name = "KAKERATTA_BLUESKY_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.bluesky_key.secret_id
            version = google_secret_manager_secret_version.bluesky_key.version
          }
        }
      }
      # The operator owns the VAPID secret; Terraform references its name
      # without reading or storing the private key in state.
      env {
        name  = "KAKERATTA_PUSH_PUBLIC_KEY"
        value = var.push_public_key
      }
      env {
        name  = "KAKERATTA_PUSH_SUBJECT"
        value = var.push_subject
      }
      env {
        name = "KAKERATTA_PUSH_PRIVATE_KEY"
        value_source {
          secret_key_ref {
            secret  = var.push_private_key_secret
            version = var.push_private_key_version
          }
        }
      }
      env {
        name  = "KAKERATTA_HOME"
        value = "/tmp/kakeratta"
      }
      # The operator registers this secret separately; only its reference
      # enters Terraform state, never the WeatherAPI key itself.
      env {
        name = "KAKERATTA_WEATHERAPI_KEY"
        value_source {
          secret_key_ref {
            secret  = var.weatherapi_secret_id
            version = var.weatherapi_secret_version
          }
        }
      }
      # Wakes: the queue, where a task calls back, and the account it carries
      # a token for. The URL is the service's own, which Cloud Run names from
      # the project number.
      env {
        name  = "KAKERATTA_TASKS_QUEUE"
        value = google_cloud_tasks_queue.due.id
      }
      env {
        name  = "KAKERATTA_TASKS_URL"
        value = "https://${local.name}-${data.google_project.current.number}.${var.run_region}.run.app/internal/due"
      }
      env {
        name  = "KAKERATTA_TASKS_ACCOUNT"
        value = google_service_account.app.email
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
      env {
        name = "KAKERATTA_JUDGE_INVITE_TOKEN"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.judge_invite.secret_id
            version = google_secret_manager_secret_version.judge_invite.version
          }
        }
      }
      env {
        name = "KAKERATTA_JUDGE_TOKEN_FACTORY_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.judge_token_factory_key.secret_id
            version = google_secret_manager_secret_version.judge_token_factory_key.version
          }
        }
      }
      env {
        name  = "KAKERATTA_JUDGE_EXPIRES_AT"
        value = var.judge_expires_at
      }
      env {
        name  = "KAKERATTA_JUDGE_MAX_USES"
        value = tostring(var.judge_max_uses)
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
  depends_on = [google_project_iam_member.app_roles, google_storage_bucket_iam_member.app_blobs]
}
