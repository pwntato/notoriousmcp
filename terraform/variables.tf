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

variable "domain_name" {
  type        = string
  default     = ""
  description = "Optional custom domain (e.g. notoriousmcp.com). Leave empty to use CloudFront URL."
}

variable "domain_contact_first_name" {
  type    = string
  default = ""
}

variable "domain_contact_last_name" {
  type    = string
  default = ""
}

variable "domain_contact_organization" {
  type    = string
  default = ""
}

variable "domain_contact_address" {
  type    = string
  default = ""
}

variable "domain_contact_city" {
  type    = string
  default = ""
}

variable "domain_contact_state" {
  type    = string
  default = ""
}

variable "domain_contact_zip" {
  type    = string
  default = ""
}

variable "domain_contact_country_code" {
  type    = string
  default = "US"
}

variable "domain_contact_phone" {
  type        = string
  default     = ""
  description = "E.164 format, e.g. +1.5555550100"
}

variable "domain_contact_email" {
  type    = string
  default = ""
}
