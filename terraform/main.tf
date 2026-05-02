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

# ACM for CloudFront must be in us-east-1 regardless of deployment region
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}
