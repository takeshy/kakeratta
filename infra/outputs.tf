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

output "cloud_run_url" {
  description = "Cloud Run backend URL."
  value       = google_cloud_run_v2_service.app.uri
}

output "judge_url" {
  description = "Shared registration URL for hackathon judges."
  value       = "https://${local.domain}/judge/${random_id.judge_invite.hex}"
  sensitive   = true
}
