variable "project_id" {
  description = "Google Cloud project ID."
  type        = string
  default     = "kakeratta"
}

variable "region" {
  description = "Google Cloud region."
  type        = string
  default     = "asia-northeast1"
}

variable "run_region" {
  description = "Cloud Run region supported by Firebase Hosting rewrites."
  type        = string
  default     = "asia-east1"
}

variable "zone" {
  description = "Google Cloud zone for the single VM."
  type        = string
  default     = "asia-northeast1-b"
}

variable "domain" {
  description = "Public domain served by kakeratta and managed in Cloud DNS."
  type        = string
  default     = "kakeratta.net"
}

variable "mcp_servers" {
  description = "MCP servers whose tools every persona may declare, as server.tool. Headers are sent on every request (for example an Authorization header)."
  type = list(object({
    name    = string
    url     = string
    headers = optional(map(string), {})
  }))
  default   = []
  sensitive = true
}

variable "image_tag" {
  description = "Container image tag set by deploy.sh."
  type        = string
  default     = "latest"
}

variable "firebase_project_id" {
  description = "Firebase project ID for Google sign-in. Required for deployment."
  type        = string
  validation {
    condition     = trimspace(var.firebase_project_id) != ""
    error_message = "Firebase authentication is required for deployment."
  }
}

variable "firebase_api_key" {
  description = "Public Firebase Web API key."
  type        = string
  default     = ""
}

variable "firebase_auth_domain" {
  description = "Firebase Auth domain, for example PROJECT.firebaseapp.com."
  type        = string
  default     = ""
}

variable "bluesky_public_url" {
  description = "Canonical public HTTPS origin for Bluesky OAuth. Empty uses https://domain."
  type        = string
  default     = ""
  validation {
    condition     = var.bluesky_public_url == "" || can(regex("^https://[^/?#@]+$", var.bluesky_public_url))
    error_message = "Use an HTTPS origin without a path, query or fragment."
  }
}

variable "weatherapi_secret_id" {
  description = "Existing Secret Manager secret containing the WeatherAPI key; Terraform does not manage its value."
  type        = string
  default     = "kakeratta-wheatherapi-key"
}

variable "weatherapi_secret_version" {
  description = "WeatherAPI secret version resolved when a Cloud Run instance starts."
  type        = string
  default     = "latest"
}

variable "judge_mcp_key_secret_id" {
  description = "Existing Secret Manager secret containing the 64-character shared judge MCP key; Terraform does not manage its value."
  type        = string
  default     = "kakeratta-judge-mcp-key"
}

variable "judge_mcp_key_secret_version" {
  description = "Shared judge MCP key secret version resolved when a Cloud Run instance starts."
  type        = string
  default     = "latest"
}

variable "judge_token_factory_key" {
  description = "Server-side Nebius Token Factory key used only by judge accounts."
  type        = string
  sensitive   = true
  default     = ""
}

variable "judge_expires_at" {
  description = "RFC3339 end of judge registration and access."
  type        = string
  default     = "2026-12-15T20:00:00Z"
}

variable "judge_max_uses" {
  description = "Maximum charged AI actions per judge account."
  type        = number
  default     = 200
}

variable "max_instances" {
  description = "Most Cloud Run instances serving the API at once. Persona work is claimed reaction by reaction in Firestore, so any instance may take it up."
  type        = number
  default     = 2
}

variable "push_public_key" {
  description = "Base64url VAPID public key matching the operator-managed private key."
  type        = string
  default     = "BK6kXDIPQQhbN7d8pchRzEpzk-0nJkPPjS6G8xv5I2U86JGkI6s1x1uBVHTU9F8UTRrjgWUgVKZUs54xjcXAFLA"
}

variable "push_subject" {
  description = "VAPID operator contact as a mailto or HTTPS URI."
  type        = string
  default     = "mailto:takeshy.work@gmail.com"
}

variable "push_private_key_secret" {
  description = "Existing Secret Manager secret containing the base64url VAPID private key."
  type        = string
  default     = "kakeratta-vapid-private"
}

variable "push_private_key_version" {
  description = "Secret Manager version of the VAPID private key."
  type        = string
  default     = "latest"
}

variable "queue_worker_enabled" {
  description = "Enable the scale-to-zero Pub/Sub push worker."
  type        = bool
  default     = true
}
variable "shared_vm_ip" {
  description = "Shared HTTPS load balancer IP; empty retains Firebase/Cloud Run routing."
  type        = string
  default     = ""
}
variable "worker_max_instances" {
  description = "Maximum concurrent push worker instances; each processes one job."
  type        = number
  default     = 1
  validation {
    condition     = var.worker_max_instances >= 1 && floor(var.worker_max_instances) == var.worker_max_instances
    error_message = "worker_max_instances must be a positive integer."
  }
}
