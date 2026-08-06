# Production environment — single droplet running the mini profile.
environment  = "production"
region       = "sgp1"
droplet_size = "s-4vcpu-8gb"

ssh_key_fingerprints = []
ssh_public_keys      = []

# DNS (optional). Set both to have Caddy get a real cert for app.<domain>.
domain    = ""
subdomain = "app"

# Narrow this to trusted admin/CI ranges for production.
allowed_ssh_cidrs  = ["0.0.0.0/0"]
enable_backups     = true
enable_reserved_ip = true
