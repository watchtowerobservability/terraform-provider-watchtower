resource "watchtower_attribute_registry" "tenant" {
  name = "app.tenant"
  spec = jsonencode({ signal = "logs", pii_class = "none", mode = "warn" })
}

resource "watchtower_archive_config" "logs" {
  name = "logs"
  spec = jsonencode({ bucket = "wt-archive", credentials = { access_key_id = "$${AWS_AK}", secret_access_key = "$${AWS_SK}" } })
}

resource "watchtower_retention_rule" "logs_30d" {
  name = "10" # priority
  spec = jsonencode({ signal = "logs", days = 30, match = {} })
}

resource "watchtower_change_source" "flags" {
  name = "unleash" # provider kind; the one-time secret is in the sensitive token attribute
  spec = jsonencode({ mapping = {} })
}

resource "watchtower_ip_allowlist" "ingest" {
  project = "org_..." # org-scoped: holds the org id (or set provider org)
  name    = "ingest"
  spec    = jsonencode({ cidrs = ["10.0.0.0/8"], enabled = true })
}
