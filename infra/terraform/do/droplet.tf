locals {
  name        = "nexus-${var.environment}"
  common_tags = concat(["nexus", var.environment], var.tags)
}

# Register any raw public keys provided, and combine with pre-uploaded fingerprints.
resource "digitalocean_ssh_key" "provided" {
  for_each   = { for idx, key in var.ssh_public_keys : idx => key }
  name       = "${local.name}-${each.key}"
  public_key = each.value
}

locals {
  ssh_keys = concat(
    var.ssh_key_fingerprints,
    [for k in digitalocean_ssh_key.provided : k.fingerprint],
  )
}

resource "digitalocean_droplet" "app" {
  name       = local.name
  region     = var.region
  size       = var.droplet_size
  image      = var.droplet_image
  ssh_keys   = local.ssh_keys
  backups    = var.enable_backups
  monitoring = true
  tags       = local.common_tags

  # First-boot host prep: install Docker Engine + compose plugin so the deploy
  # workflow can immediately `docker compose pull/up`. Full host hardening and the
  # nexus-sandboxd systemd unit are handled by scripts/bootstrap-droplet.sh.
  user_data = <<-CLOUDINIT
    #cloud-config
    package_update: true
    runcmd:
      - curl -fsSL https://get.docker.com | sh
      - usermod -aG docker root
      - mkdir -p /opt/nexus /run/nexus
      - systemctl enable --now docker
  CLOUDINIT

  lifecycle {
    ignore_changes = [image] # avoid destroy/recreate when DO refreshes the base image
  }
}

resource "digitalocean_reserved_ip" "app" {
  count      = var.enable_reserved_ip ? 1 : 0
  droplet_id = digitalocean_droplet.app.id
  region     = var.region
}
