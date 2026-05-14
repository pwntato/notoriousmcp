resource "aws_cloudfront_function" "restore_www_authenticate" {
  name    = "notoriousmcp-restore-www-authenticate"
  runtime = "cloudfront-js-2.0"
  publish = true
  comment = "Restore WWW-Authenticate header stripped by CloudFront Lambda origin handling"

  code = <<-EOF
    async function handler(event) {
      const response = event.response;
      const headers = response.headers;
      if (headers["x-amzn-remapped-www-authenticate"]) {
        headers["www-authenticate"] = { value: headers["x-amzn-remapped-www-authenticate"].value };
        delete headers["x-amzn-remapped-www-authenticate"];
      }
      return response;
    }
  EOF
}
