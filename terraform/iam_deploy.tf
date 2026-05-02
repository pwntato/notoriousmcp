data "aws_caller_identity" "current" {}

# OIDC provider for GitHub Actions — create once per AWS account
# If you already have this provider, import it rather than creating a new one:
#   terraform import aws_iam_openid_connect_provider.github arn:aws:iam::<account_id>:oidc-provider/token.actions.githubusercontent.com
resource "aws_iam_openid_connect_provider" "github" {
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
  # AWS ignores thumbprint_list for token.actions.githubusercontent.com — required by the
  # resource schema but has no effect; any non-empty value satisfies the provider.
  thumbprint_list = ["0000000000000000000000000000000000000000"]
}

data "aws_iam_policy_document" "deploy_assume_role" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:pwntato/notoriousmcp:ref:refs/heads/main"]
    }
  }
}

resource "aws_iam_role" "deploy" {
  name               = "notoriousmcp-deploy-${var.environment}"
  assume_role_policy = data.aws_iam_policy_document.deploy_assume_role.json
}

data "aws_iam_policy_document" "deploy_policy" {
  statement {
    actions   = ["lambda:UpdateFunctionCode", "lambda:GetFunction"]
    resources = [aws_lambda_function.main.arn]
  }

  statement {
    actions = [
      "lambda:CreateFunctionUrlConfig",
      "lambda:UpdateFunctionUrlConfig",
      "lambda:GetFunctionUrlConfig",
      "lambda:AddPermission",
      "lambda:RemovePermission",
    ]
    resources = [aws_lambda_function.main.arn]
  }

  statement {
    actions = [
      "cloudfront:CreateInvalidation",
      "cloudfront:GetDistribution",
      "cloudfront:UpdateDistribution",
    ]
    resources = [aws_cloudfront_distribution.main.arn]
  }

  statement {
    # ssm:PutParameter allows CI to rotate OAuth credentials and admin IDs via terraform apply.
    # This is intentional — Terraform manages these values — but means anyone who can push to
    # main (currently only pwntato) can overwrite production secrets.
    actions = [
      "ssm:PutParameter",
      "ssm:GetParameter",
    ]
    resources = [
      aws_ssm_parameter.google_client_id.arn,
      aws_ssm_parameter.google_client_secret.arn,
      aws_ssm_parameter.admin_google_ids.arn,
    ]
  }

  statement {
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:ListBucket",
    ]
    resources = [
      "arn:aws:s3:::${var.state_bucket}",
      "arn:aws:s3:::${var.state_bucket}/*",
    ]
  }

  statement {
    actions = [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:DeleteItem",
    ]
    resources = ["arn:aws:dynamodb:${var.aws_region}:${data.aws_caller_identity.current.account_id}:table/${var.state_bucket}-lock"]
  }
}

resource "aws_iam_role_policy" "deploy" {
  name   = "notoriousmcp-deploy-policy"
  role   = aws_iam_role.deploy.id
  policy = data.aws_iam_policy_document.deploy_policy.json
}

output "deploy_role_arn" {
  value       = aws_iam_role.deploy.arn
  description = "Set this as the AWS_DEPLOY_ROLE_ARN GitHub Actions secret"
}
