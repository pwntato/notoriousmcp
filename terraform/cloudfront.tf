locals {
  lambda_url_host = trimsuffix(replace(aws_lambda_function_url.main.function_url, "https://", ""), "/")
  cf_origin_id    = "lambda"
}

resource "aws_cloudfront_origin_access_control" "lambda" {
  name                              = "notoriousmcp-lambda-oac-${var.environment}"
  origin_access_control_origin_type = "lambda"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_cloudfront_distribution" "main" {
  enabled         = true
  is_ipv6_enabled = true
  price_class     = "PriceClass_100"

  aliases = var.domain_name != "" ? [var.domain_name] : []

  origin {
    domain_name              = local.lambda_url_host
    origin_id                = local.cf_origin_id
    origin_access_control_id = aws_cloudfront_origin_access_control.lambda.id

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }

  default_cache_behavior {
    target_origin_id       = local.cf_origin_id
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods         = ["GET", "HEAD"]

    cache_policy_id          = "4135ea2d-6df8-44a3-9df3-4b5a84be39ad" # CachingDisabled
    origin_request_policy_id = "216adef6-5c7f-47e4-b989-5492eafa07d3" # AllViewer
  }

  viewer_certificate {
    acm_certificate_arn            = var.domain_name != "" ? aws_acm_certificate_validation.main[0].certificate_arn : null
    cloudfront_default_certificate = var.domain_name == ""
    ssl_support_method             = var.domain_name != "" ? "sni-only" : null
    minimum_protocol_version       = var.domain_name != "" ? "TLSv1.2_2021" : null
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }
}

output "cloudfront_url" {
  value = "https://${aws_cloudfront_distribution.main.domain_name}"
}
