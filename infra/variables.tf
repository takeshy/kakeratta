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

variable "machine_type" {
  description = "Compute Engine machine type."
  type        = string
  default     = "e2-small"
}

variable "data_disk_size_gb" {
  description = "Persistent disk size for posts, images, settings, and persona memories."
  type        = number
  default     = 20
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
