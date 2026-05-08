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

variable "google_client_id" {
  type      = string
  sensitive = true
}

variable "google_client_secret" {
  type      = string
  sensitive = true
}

variable "admin_google_ids" {
  type        = string
  sensitive   = true
  description = "Comma-separated Google subject IDs for bootstrap admins"
}

variable "token_secret" {
  type        = string
  sensitive   = true
  description = "HMAC signing secret for access tokens (min 32 bytes)"
}

variable "redirect_url" {
  type        = string
  description = "Full OAuth callback URL registered in Google Cloud Console (e.g. https://example.com/auth/callback)"
}

variable "domain_name" {
  type        = string
  default     = ""
  description = "Optional custom domain (e.g. notoriousmcp.com). Leave empty to use CloudFront URL."
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
