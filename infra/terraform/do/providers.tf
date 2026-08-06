# The DO token is read from the DIGITALOCEAN_TOKEN environment variable in CI,
# so it is intentionally not declared as a variable here.
provider "digitalocean" {}
