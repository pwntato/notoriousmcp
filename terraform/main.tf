terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  backend "s3" {
    # All values passed via -backend-config at init time (see deploy.yml / README)
    # bucket and dynamodb_table are derived from the bootstrap state_bucket_name output
    key    = "notoriousmcp/terraform.tfstate"
    region = "us-east-1"
  }
}

provider "aws" {
  region = var.aws_region
}
