variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "environment" {
  type    = string
  default = "prod"
}

variable "table_name" {
  type    = string
  default = "notoriousmcp"
}

variable "state_bucket" {
  type        = string
  description = "S3 bucket name for Terraform remote state (created by bootstrap)"
}

variable "oauth_provider" {
  type        = string
  default     = "google"
  description = "OAuth provider to use: \"google\" (default) or \"okta\""
  validation {
    condition     = contains(["google", "okta"], var.oauth_provider)
    error_message = "oauth_provider must be \"google\" or \"okta\"."
  }
}

variable "okta_domain" {
  type        = string
  default     = ""
  description = "Okta domain (e.g. dev-123.okta.com). Required when oauth_provider is \"okta\"."
  validation {
    condition     = var.oauth_provider != "okta" || var.okta_domain != ""
    error_message = "okta_domain is required when oauth_provider is \"okta\"."
  }
}

variable "auto_approve_users" {
  type        = bool
  default     = false
  description = "Auto-approve new users on first login (set to true for Okta deployments where any authenticated user is an employee)."
}

variable "oauth_client_id" {
  type      = string
  sensitive = true
}

variable "oauth_client_secret" {
  type      = string
  sensitive = true
}

variable "admin_ids" {
  type        = string
  sensitive   = true
  description = "Comma-separated provider subject IDs (sub claim) for bootstrap admins"
}

variable "token_secret" {
  type        = string
  sensitive   = true
  description = "HMAC signing secret for access tokens (min 32 bytes)"
  validation {
    condition     = length(var.token_secret) >= 32
    error_message = "token_secret must be at least 32 bytes."
  }
}

variable "redirect_url" {
  type        = string
  description = "Full OAuth callback URL registered in Google Cloud Console (e.g. https://example.com/auth/callback)"
  validation {
    condition     = endswith(var.redirect_url, "/auth/callback")
    error_message = "redirect_url must end with /auth/callback."
  }
}

variable "public_base_url" {
  type        = string
  default     = ""
  description = "Public base URL of the service (e.g. https://abc123.execute-api.us-west-2.amazonaws.com). Defaults to redirect_url with /auth/callback stripped."
  validation {
    condition     = var.public_base_url == "" || startswith(var.public_base_url, "https://")
    error_message = "public_base_url must start with https:// or be left empty."
  }
}

variable "domain_name" {
  type        = string
  default     = ""
  description = "Optional custom domain (e.g. notoriousmcp.com). Leave empty to use the API Gateway invoke URL."
}

variable "domain_contact_first_name" {
  type    = string
  default = ""
  validation {
    condition     = var.domain_name == "" || var.domain_contact_first_name != ""
    error_message = "domain_contact_first_name is required when domain_name is set."
  }
}

variable "domain_contact_last_name" {
  type    = string
  default = ""
  validation {
    condition     = var.domain_name == "" || var.domain_contact_last_name != ""
    error_message = "domain_contact_last_name is required when domain_name is set."
  }
}

variable "domain_contact_organization" {
  type    = string
  default = ""
}

variable "domain_contact_address" {
  type    = string
  default = ""
  validation {
    condition     = var.domain_name == "" || var.domain_contact_address != ""
    error_message = "domain_contact_address is required when domain_name is set."
  }
}

variable "domain_contact_city" {
  type    = string
  default = ""
  validation {
    condition     = var.domain_name == "" || var.domain_contact_city != ""
    error_message = "domain_contact_city is required when domain_name is set."
  }
}

variable "domain_contact_state" {
  type    = string
  default = ""
  validation {
    condition     = var.domain_name == "" || var.domain_contact_state != ""
    error_message = "domain_contact_state is required when domain_name is set."
  }
}

variable "domain_contact_zip" {
  type    = string
  default = ""
  validation {
    condition     = var.domain_name == "" || var.domain_contact_zip != ""
    error_message = "domain_contact_zip is required when domain_name is set."
  }
}

variable "domain_contact_country_code" {
  type    = string
  default = "US"
}

variable "domain_contact_phone" {
  type        = string
  default     = ""
  description = "E.164 format, e.g. +1.5555550100"
  validation {
    condition     = var.domain_name == "" || var.domain_contact_phone != ""
    error_message = "domain_contact_phone is required when domain_name is set."
  }
}

variable "domain_contact_email" {
  type    = string
  default = ""
  validation {
    condition     = var.domain_name == "" || var.domain_contact_email != ""
    error_message = "domain_contact_email is required when domain_name is set."
  }
}
