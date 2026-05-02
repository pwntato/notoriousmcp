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

resource "aws_lambda_function" "main" {
  function_name                  = "notoriousmcp-${var.environment}"
  role                           = aws_iam_role.lambda.arn
  handler                        = "bootstrap"
  runtime                        = "provided.al2023"
  architectures                  = ["arm64"]
  filename                       = "${path.root}/../lambda.zip"
  timeout                        = 30
  memory_size                    = 256
  reserved_concurrent_executions = 10

  environment {
    variables = {
      TABLE_NAME               = var.table_name
      S3_BUCKET                = aws_s3_bucket.content.bucket
      ENVIRONMENT              = var.environment
      SSM_GOOGLE_CLIENT_ID     = aws_ssm_parameter.google_client_id.name
      SSM_GOOGLE_CLIENT_SECRET = aws_ssm_parameter.google_client_secret.name
      SSM_ADMIN_GOOGLE_IDS     = aws_ssm_parameter.admin_google_ids.name
    }
  }

  depends_on = [aws_cloudwatch_log_group.lambda]
}
