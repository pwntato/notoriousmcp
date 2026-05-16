resource "aws_ssm_parameter" "oauth_client_id" {
  name  = "/notoriousmcp/${var.environment}/oauth_client_id"
  type  = "SecureString"
  value = var.oauth_client_id
}

resource "aws_ssm_parameter" "oauth_client_secret" {
  name  = "/notoriousmcp/${var.environment}/oauth_client_secret"
  type  = "SecureString"
  value = var.oauth_client_secret
}

resource "aws_ssm_parameter" "admin_ids" {
  name  = "/notoriousmcp/${var.environment}/admin_ids"
  type  = "SecureString"
  value = var.admin_ids
}

resource "aws_ssm_parameter" "token_secret" {
  name  = "/notoriousmcp/${var.environment}/token_secret"
  type  = "SecureString"
  value = var.token_secret
}
