output "url" {
  description = "HTTPS URL for kakeratta."
  value       = "https://${local.domain}"
}

output "image" {
  description = "Container image built by deploy.sh."
  value       = local.image
}

output "instance" {
  description = "Compute Engine instance name."
  value       = google_compute_instance.app.name
}

output "zone" {
  description = "Compute Engine zone."
  value       = var.zone
}

output "login_url" {
  description = "One-time browser login URL. Keep this value private."
  value       = "https://${local.domain}/?token=${urlencode(local.access_token)}"
  sensitive   = true
}
