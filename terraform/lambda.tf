data "aws_iam_policy_document" "lambda_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "lambda" {
  name               = "notoriousmcp-lambda-${var.environment}"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume_role.json
}

data "aws_iam_policy_document" "lambda_policy" {
  statement {
    actions = [
      "logs:CreateLogGroup",
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["arn:aws:logs:*:*:*"]
  }

  statement {
    actions = [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:UpdateItem",
      "dynamodb:DeleteItem",
      "dynamodb:Query",
      "dynamodb:Scan",
    ]
    resources = [
      aws_dynamodb_table.main.arn,
      "${aws_dynamodb_table.main.arn}/index/*",
    ]
  }

  statement {
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
    ]
    resources = ["${aws_s3_bucket.content.arn}/*"]
  }

  statement {
    actions = ["ssm:GetParameter"]
    resources = [
      aws_ssm_parameter.google_client_id.arn,
      aws_ssm_parameter.google_client_secret.arn,
      aws_ssm_parameter.admin_google_ids.arn,
      aws_ssm_parameter.token_secret.arn,
    ]
  }
}

resource "aws_iam_role_policy" "lambda" {
  name   = "notoriousmcp-lambda-policy"
  role   = aws_iam_role.lambda.id
  policy = data.aws_iam_policy_document.lambda_policy.json
}

resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/notoriousmcp-${var.environment}"
  retention_in_days = 14
}

# reserved_concurrent_executions omitted — account limit is 10 total; reserving any
# would drop unreserved below the 10-minimum and fail. The 10-total cap already
# bounds abuse. Revisit if the account limit is raised.
resource "aws_lambda_function" "main" {
  function_name    = "notoriousmcp-${var.environment}"
  role             = aws_iam_role.lambda.arn
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  filename         = "${path.root}/../lambda.zip"
  source_code_hash = filebase64sha256("${path.root}/../lambda.zip")
  timeout          = 30
  memory_size      = 256

  environment {
    variables = {
      TABLE_NAME               = var.table_name
      S3_BUCKET                = aws_s3_bucket.content.bucket
      ENVIRONMENT              = var.environment
      REDIRECT_URL             = var.redirect_url
      PUBLIC_BASE_URL          = local.public_base_url
      SSM_GOOGLE_CLIENT_ID     = aws_ssm_parameter.google_client_id.name
      SSM_GOOGLE_CLIENT_SECRET = aws_ssm_parameter.google_client_secret.name
      SSM_ADMIN_GOOGLE_IDS     = aws_ssm_parameter.admin_google_ids.name
      SSM_TOKEN_SECRET         = aws_ssm_parameter.token_secret.name
    }
  }

  depends_on = [aws_cloudwatch_log_group.lambda]
}
