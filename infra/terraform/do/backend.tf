# Remote state in DigitalOcean Spaces (S3-compatible). The concrete `key` and
# credentials are supplied at `terraform init` time by the CI workflow:
#
#   terraform init -backend-config="key=nexus/<env>/terraform.tfstate"
#
# with AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY set to the Spaces access keys.
# The flags below tell the S3 backend to talk to Spaces instead of AWS and to
# skip the AWS-only validation/checks that Spaces does not implement. DO Spaces
# supports conditional writes, so native state locking works without DynamoDB.
terraform {
  backend "s3" {
    bucket = "nexus-agent-tfstate"      # create this Space once, per project
    region = "us-east-1"                # ignored by Spaces but required by the backend
    endpoints = {
      s3 = "https://sgp1.digitaloceanspaces.com" # match your Spaces region
    }

    skip_credentials_validation = true
    skip_metadata_api_check     = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_s3_checksum            = true
    use_lockfile                = true
  }
}
