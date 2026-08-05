# Optional DNS: when a domain + subdomain are provided, point the environment host
# at the droplet's public address (reserved IP if enabled, else the droplet IP).
# Caddy then provisions Let's Encrypt certificates for this exact name.
locals {
  public_ip   = var.enable_reserved_ip ? digitalocean_reserved_ip.app[0].ip_address : digitalocean_droplet.app.ipv4_address
  manage_dns  = var.domain != "" && var.subdomain != ""
}

resource "digitalocean_record" "app" {
  count  = local.manage_dns ? 1 : 0
  domain = var.domain
  type   = "A"
  name   = var.subdomain
  value  = local.public_ip
  ttl    = 300
}
