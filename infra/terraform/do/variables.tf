variable "environment" {
  description = "Logical environment name (staging | production)."
  type        = string
  validation {
    condition     = contains(["staging", "production"], var.environment)
    error_message = "environment must be either \"staging\" or \"production\"."
  }
}

variable "region" {
  description = "DigitalOcean region slug (e.g. sgp1, nyc3, fra1)."
  type        = string
  default     = "sgp1"
}

variable "droplet_size" {
  description = "Droplet size slug. The mini profile (~5 services) wants >= 4GB RAM."
  type        = string
  default     = "s-2vcpu-4gb"
}

variable "droplet_image" {
  description = "Base image slug for the droplet."
  type        = string
  default     = "ubuntu-24-04-x64"
}

variable "ssh_key_fingerprints" {
  description = "Fingerprints of SSH keys (already uploaded to DO) allowed to log in."
  type        = list(string)
  default     = []
}

variable "ssh_public_keys" {
  description = "Raw SSH public keys to register with DO and authorize on the droplet."
  type        = list(string)
  default     = []
}

variable "domain" {
  description = "Root domain managed in DO DNS. Leave empty to skip DNS records."
  type        = string
  default     = ""
}

variable "subdomain" {
  description = "Subdomain (host) for this environment, e.g. \"staging\" or \"app\"."
  type        = string
  default     = ""
}

variable "allowed_ssh_cidrs" {
  description = "CIDRs permitted to reach SSH (port 22). Narrow this in production."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "tags" {
  description = "Extra tags applied to created resources."
  type        = list(string)
  default     = []
}

variable "enable_backups" {
  description = "Enable DO droplet backups."
  type        = bool
  default     = false
}

variable "enable_reserved_ip" {
  description = "Attach a reserved (floating) IP so re-provisioning keeps a stable address."
  type        = bool
  default     = true
}
