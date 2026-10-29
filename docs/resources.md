---
page_title: "Resources"
description: |-
  The shape shared by every watchtower_* resource.
---

# watchtower_* resources

Every resource is `watchtower_<kind>` (`AlertRule` becomes `watchtower_alert_rule`) and has:

- `name` (String, Required) Identity of the object inside its project (for ordered kinds, the priority).
- `spec` (String, Required) The kind's body as JSON, usually `jsonencode({...})`.
- `project` (String, Optional) Project id; for org-scoped kinds it holds the org id. Defaults to the provider value.

## Example

```terraform
resource "watchtower_ip_allowlist" "ingest" {
  project = "org_..."
  name    = "ingest"
  spec    = jsonencode({ cidrs = ["10.0.0.0/8"], enabled = true })
}
```

Secrets returned once by the API (for example change-source tokens) appear in a sensitive attribute and are
not stored anywhere else. Import and drift detection use the same `name`.
