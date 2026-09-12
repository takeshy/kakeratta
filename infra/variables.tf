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

variable "access_token" {
  description = "Optional app access token. A random token is generated when omitted."
  type        = string
  default     = null
  nullable    = true
  sensitive   = true

  validation {
    condition     = var.access_token == null || length(var.access_token) >= 20
    error_message = "access_token must contain at least 20 characters."
  }
}

variable "image_tag" {
  description = "Container image tag set by deploy.sh."
  type        = string
  default     = "latest"
}
