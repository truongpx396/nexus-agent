terraform {
  # use_lockfile (native S3 state locking, no DynamoDB) requires Terraform >= 1.10.
  required_version = ">= 1.10.0"

  required_providers {
    digitalocean = {
      source  = "digitalocean/digitalocean"
      version = "~> 2.40"
    }
  }
}
