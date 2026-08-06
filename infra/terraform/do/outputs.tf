output "droplet_id" {
  description = "DigitalOcean droplet id."
  value       = digitalocean_droplet.app.id
}

output "droplet_ipv4" {
  description = "Droplet public IPv4 (before reserved IP)."
  value       = digitalocean_droplet.app.ipv4_address
}

output "public_ip" {
  description = "The address the deploy workflow should SSH to (reserved IP if enabled)."
  value       = local.public_ip
}

output "fqdn" {
  description = "Fully-qualified host if DNS is managed, else empty."
  value       = local.manage_dns ? "${var.subdomain}.${var.domain}" : ""
}
