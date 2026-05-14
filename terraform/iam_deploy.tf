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
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:pwntato/notoriousmcp:environment:production"]
    }
  }
}

resource "aws_iam_role" "deploy" {
  name               = "notoriousmcp-deploy-${var.environment}"
  assume_role_policy = data.aws_iam_policy_document.deploy_assume_role.json
}

data "aws_iam_policy_document" "deploy_policy" {
  # Terraform plan needs read access to all managed resources to refresh state.
  # These are read-only actions scoped to this account.
  statement {
    actions = [
      "cloudfront:GetDistribution",
      "cloudfront:ListTagsForResource",
      "cloudfront:GetResponseHeadersPolicy",
      "cloudfront:ListResponseHeadersPolicies",
    ]
    resources = ["*"]
  }

  statement {
    actions = [
      "dynamodb:DescribeTable",
      "dynamodb:DescribeTimeToLive",
      "dynamodb:DescribeContinuousBackups",
      "dynamodb:ListTagsOfResource",
    ]
    resources = ["arn:aws:dynamodb:${var.aws_region}:${data.aws_caller_identity.current.account_id}:table/${aws_dynamodb_table.main.name}"]
  }

  statement {
    actions = [
      "iam:GetOpenIDConnectProvider",
      "iam:GetRole",
      "iam:GetRolePolicy",
      "iam:ListAttachedRolePolicies",
      "iam:ListRolePolicies",
    ]
    resources = ["*"]
  }

  statement {
    actions = [
      "lambda:GetFunction",
      "lambda:GetFunctionCodeSigningConfig",
      "lambda:GetFunctionUrlConfig",
      "lambda:GetPolicy",
      "lambda:ListVersionsByFunction",
    ]
    resources = [aws_lambda_function.main.arn]
  }

  statement {
    actions   = ["logs:DescribeLogGroups", "logs:ListTagsForResource"]
    resources = ["*"]
  }

  statement {
    # Use s3:Get* to cover all bucket read actions Terraform may call during refresh
    # without having to enumerate each individual action.
    actions = ["s3:Get*", "s3:List*"]
    resources = [
      "arn:aws:s3:::${aws_s3_bucket.content.bucket}",
      "arn:aws:s3:::${aws_s3_bucket.content.bucket}/*",
      "arn:aws:s3:::${var.state_bucket}",
    ]
  }

  statement {
    actions = ["ssm:GetParameter", "ssm:GetParameters", "ssm:ListTagsForResource"]
    resources = [
      aws_ssm_parameter.google_client_id.arn,
      aws_ssm_parameter.google_client_secret.arn,
      aws_ssm_parameter.admin_google_ids.arn,
      aws_ssm_parameter.token_secret.arn,
    ]
  }

  statement {
    # DescribeParameters does not support resource-level restrictions
    actions   = ["ssm:DescribeParameters"]
    resources = ["*"]
  }

  # Write permissions for terraform apply
  statement {
    actions = [
      "cloudfront:CreateInvalidation",
      "cloudfront:UpdateDistribution",
    ]
    resources = [aws_cloudfront_distribution.main.arn]
  }

  statement {
    actions = [
      "cloudfront:CreateResponseHeadersPolicy",
      "cloudfront:UpdateResponseHeadersPolicy",
      "cloudfront:DeleteResponseHeadersPolicy",
    ]
    resources = ["arn:aws:cloudfront::${data.aws_caller_identity.current.account_id}:response-headers-policy/*"]
  }

  statement {
    actions = [
      "dynamodb:CreateTable",
      "dynamodb:UpdateTable",
      "dynamodb:DeleteTable",
      "dynamodb:UpdateTimeToLive",
      "dynamodb:TagResource",
      "dynamodb:UntagResource",
    ]
    resources = ["arn:aws:dynamodb:${var.aws_region}:${data.aws_caller_identity.current.account_id}:table/${aws_dynamodb_table.main.name}"]
  }

  statement {
    actions = [
      "iam:PutRolePolicy",
      "iam:DeleteRolePolicy",
      "iam:UpdateAssumeRolePolicy",
    ]
    resources = [
      aws_iam_role.deploy.arn,
      aws_iam_role.lambda.arn,
    ]
  }

  statement {
    # OIDC provider actions require "*" — AWS does not support resource-level restrictions for OIDCP ARNs.
    actions = [
      "iam:CreateOpenIDConnectProvider",
      "iam:UpdateOpenIDConnectProvider",
      "iam:DeleteOpenIDConnectProvider",
      "iam:TagOpenIDConnectProvider",
    ]
    resources = ["*"]
  }

  statement {
    actions = [
      "lambda:UpdateFunctionCode",
      "lambda:UpdateFunctionConfiguration",
      "lambda:AddPermission",
      "lambda:RemovePermission",
      "lambda:CreateFunctionUrlConfig",
      "lambda:UpdateFunctionUrlConfig",
      "lambda:TagResource",
    ]
    resources = [aws_lambda_function.main.arn]
  }

  statement {
    actions = [
      "logs:CreateLogGroup",
      "logs:PutRetentionPolicy",
      "logs:DeleteLogGroup",
      "logs:TagLogGroup",
      "logs:TagResource",
    ]
    resources = ["arn:aws:logs:*:${data.aws_caller_identity.current.account_id}:log-group:*"]
  }

  statement {
    actions = [
      "s3:PutEncryptionConfiguration",
      "s3:PutBucketVersioning",
      "s3:PutBucketPublicAccessBlock",
      "s3:PutLifecycleConfiguration",
      "s3:PutBucketTagging",
      "s3:DeleteBucketEncryption",
      "s3:DeletePublicAccessBlock",
    ]
    resources = ["arn:aws:s3:::${aws_s3_bucket.content.bucket}"]
  }

  statement {
    # ssm:PutParameter allows CI to rotate OAuth credentials and admin IDs via terraform apply.
    # This is intentional — Terraform manages these values — but means anyone who can push to
    # main (currently only pwntato) can overwrite production secrets.
    actions = ["ssm:PutParameter", "ssm:AddTagsToResource"]
    resources = [
      aws_ssm_parameter.google_client_id.arn,
      aws_ssm_parameter.google_client_secret.arn,
      aws_ssm_parameter.admin_google_ids.arn,
      aws_ssm_parameter.token_secret.arn,
    ]
  }

  # Terraform state backend
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
    # State lock table is always in us-east-1 (created by bootstrap, which has no region variable)
    resources = ["arn:aws:dynamodb:us-east-1:${data.aws_caller_identity.current.account_id}:table/${var.state_bucket}-lock"]
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
