resource "aws_cloudfront_response_headers_policy" "www_authenticate" {
  name    = "notoriousmcp-www-authenticate"
  comment = "Inject WWW-Authenticate on all responses; CloudFront strips it from Lambda origin responses"

  custom_headers_config {
    items {
      header   = "WWW-Authenticate"
      value    = "Bearer resource_metadata=\"${local.public_base_url}/.well-known/oauth-protected-resource\""
      override = true
    }
  }
}
