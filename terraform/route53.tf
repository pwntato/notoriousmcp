# Only provisioned when domain_name is set

resource "aws_route53domains_registered_domain" "main" {
  count       = var.domain_name != "" ? 1 : 0
  domain_name = var.domain_name

  dynamic "registrant_contact" {
    for_each = [1]
    content {
      first_name        = var.domain_contact_first_name
      last_name         = var.domain_contact_last_name
      organization_name = var.domain_contact_organization
      address_line_1    = var.domain_contact_address
      city              = var.domain_contact_city
      state             = var.domain_contact_state
      zip_code          = var.domain_contact_zip
      country_code      = var.domain_contact_country_code
      phone_number      = var.domain_contact_phone
      email             = var.domain_contact_email
    }
  }

  dynamic "admin_contact" {
    for_each = [1]
    content {
      first_name        = var.domain_contact_first_name
      last_name         = var.domain_contact_last_name
      organization_name = var.domain_contact_organization
      address_line_1    = var.domain_contact_address
      city              = var.domain_contact_city
      state             = var.domain_contact_state
      zip_code          = var.domain_contact_zip
      country_code      = var.domain_contact_country_code
      phone_number      = var.domain_contact_phone
      email             = var.domain_contact_email
    }
  }

  dynamic "tech_contact" {
    for_each = [1]
    content {
      first_name        = var.domain_contact_first_name
      last_name         = var.domain_contact_last_name
      organization_name = var.domain_contact_organization
      address_line_1    = var.domain_contact_address
      city              = var.domain_contact_city
      state             = var.domain_contact_state
      zip_code          = var.domain_contact_zip
      country_code      = var.domain_contact_country_code
      phone_number      = var.domain_contact_phone
      email             = var.domain_contact_email
    }
  }
}

resource "aws_route53_zone" "main" {
  count = var.domain_name != "" ? 1 : 0
  name  = var.domain_name
}

resource "aws_route53_record" "apex" {
  count   = var.domain_name != "" ? 1 : 0
  zone_id = aws_route53_zone.main[0].zone_id
  name    = var.domain_name
  type    = "A"

  alias {
    name                   = aws_apigatewayv2_domain_name.main[0].domain_name_configuration[0].target_domain_name
    zone_id                = aws_apigatewayv2_domain_name.main[0].domain_name_configuration[0].hosted_zone_id
    evaluate_target_health = false
  }
}
