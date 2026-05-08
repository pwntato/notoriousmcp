resource "aws_ssm_parameter" "google_client_id" {
  name  = "/notoriousmcp/${var.environment}/google_client_id"
  type  = "SecureString"
  value = var.google_client_id
}

resource "aws_ssm_parameter" "google_client_secret" {
  name  = "/notoriousmcp/${var.environment}/google_client_secret"
  type  = "SecureString"
  value = var.google_client_secret
}

resource "aws_ssm_parameter" "admin_google_ids" {
  name  = "/notoriousmcp/${var.environment}/admin_google_ids"
  type  = "SecureString"
  value = var.admin_google_ids
}

resource "aws_ssm_parameter" "token_secret" {
  name  = "/notoriousmcp/${var.environment}/token_secret"
  type  = "SecureString"
  value = var.token_secret
}
