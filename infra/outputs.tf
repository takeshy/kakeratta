output "url" {
  description = "HTTPS URL for kakeratta."
  value       = "https://${local.domain}"
}

output "fallback_url" {
  description = "HTTPS URL that works while custom-domain DNS is being delegated."
  value       = "https://${local.fallback_domain}"
}

output "name_servers" {
  description = "Authoritative name servers to configure at the domain registrar."
  value       = google_dns_managed_zone.app.name_servers
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
