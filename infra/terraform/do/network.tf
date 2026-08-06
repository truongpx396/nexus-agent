# Cloud firewall: only SSH (restrictable per environment), HTTP and HTTPS reach the
# droplet. Caddy terminates TLS on 443 and redirects 80 -> 443. Everything else
# (Postgres, Redis, OpenBao, Casdoor, the app's internal ports) stays on the
# docker network and is never exposed to the internet.
resource "digitalocean_firewall" "app" {
  name        = "${local.name}-fw"
  droplet_ids = [digitalocean_droplet.app.id]
  tags        = local.common_tags

  inbound_rule {
    protocol         = "tcp"
    port_range       = "22"
    source_addresses = var.allowed_ssh_cidrs
  }

  inbound_rule {
    protocol         = "tcp"
    port_range       = "80"
    source_addresses = ["0.0.0.0/0", "::/0"]
  }

  inbound_rule {
    protocol         = "tcp"
    port_range       = "443"
    source_addresses = ["0.0.0.0/0", "::/0"]
  }

  # Allow all outbound (image pulls, ACME, model-provider APIs).
  outbound_rule {
    protocol              = "tcp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }

  outbound_rule {
    protocol              = "udp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }

  outbound_rule {
    protocol              = "icmp"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }
}
