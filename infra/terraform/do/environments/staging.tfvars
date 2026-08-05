# Staging environment — single droplet running the mini profile.
environment  = "staging"
region       = "sgp1"
droplet_size = "s-2vcpu-4gb"

# Uploaded-key fingerprints OR raw public keys (either works; combine if needed).
ssh_key_fingerprints = []
ssh_public_keys      = []

# DNS (optional). Set both to have Caddy get a real cert for staging.<domain>.
domain    = ""
subdomain = "staging"

# Lock SSH down to your CI egress / office ranges when known.
allowed_ssh_cidrs  = ["0.0.0.0/0"]
enable_backups     = false
enable_reserved_ip = true
