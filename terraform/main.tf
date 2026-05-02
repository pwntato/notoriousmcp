terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  backend "s3" {
    # Populated at init time via -backend-config or TF_VAR_state_bucket
    key            = "notoriousmcp/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "notoriousmcp-tfstate-lock"
  }
}

provider "aws" {
  region = var.aws_region
}

# ACM for CloudFront must be in us-east-1 regardless of deployment region
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}
