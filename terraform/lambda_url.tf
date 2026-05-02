resource "aws_lambda_function_url" "main" {
  function_name      = aws_lambda_function.main.function_name
  authorization_type = "AWS_IAM"
  invoke_mode        = "RESPONSE_STREAM"
}

# Allow CloudFront OAC to invoke the Lambda URL
resource "aws_lambda_permission" "cloudfront" {
  statement_id  = "AllowCloudFrontInvoke"
  action        = "lambda:InvokeFunctionUrl"
  function_name = aws_lambda_function.main.function_name
  principal     = "cloudfront.amazonaws.com"
  source_arn    = aws_cloudfront_distribution.main.arn

  function_url_auth_type = "AWS_IAM"
}
