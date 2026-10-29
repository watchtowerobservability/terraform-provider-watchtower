# terraform-provider-watchtower

[![CI](https://github.com/watchtowerobservability/terraform-provider-watchtower/actions/workflows/ci.yml/badge.svg)](https://github.com/watchtowerobservability/terraform-provider-watchtower/actions/workflows/ci.yml)

Manage Watchtower configuration (alert rules, dashboards, retention, archive, access lists, ...) as
Terraform resources. Every resource has the same shape: `project` (or the org id for org-scoped kinds),
`name`, and `spec` as JSON, and maps onto the public `/v1` API. This repository is a snapshot mirror of the
Watchtower monorepo.

## Install

```hcl
terraform {
  required_providers {
    watchtower = {
      source  = "watchtowerobservability/watchtower"
      version = "~> 0.1"
    }
  }
}
```

## Quick start

```hcl
provider "watchtower" {} # WT_API, WT_API_KEY, WT_PROJECT, WT_ORG from the environment

resource "watchtower_retention_rule" "logs_30d" {
  name = "10"
  spec = jsonencode({ signal = "logs", days = 30, match = {} })
}
```

## Resources

One `watchtower_<snake_case_kind>` resource per as-code kind, for example `watchtower_alert_rule`,
`watchtower_retention_rule`, `watchtower_archive_config`, `watchtower_ip_allowlist`. See `docs/` and
`terraform/examples/main.tf` for complete examples. Inaccessible resources read as not found (404).

## Configuration

| Argument | Environment | Meaning |
| --- | --- | --- |
| `endpoint` | `WT_API` | API base URL |
| `api_key` | `WT_API_KEY` | Secret key, sent as `Authorization: Bearer`, sensitive, never logged |
| `project` | `WT_PROJECT` | Default project id |
| `org` | `WT_ORG` | Default org id for org-scoped resources |

## Verify a download

```sh
gh attestation verify terraform-provider-watchtower_0.1.0_linux_amd64.zip --repo watchtowerobservability/terraform-provider-watchtower
```

Releases also ship `SHA256SUMS` and, when the maintainers' signing key is configured, `SHA256SUMS.sig`.

## License

Apache-2.0, see `LICENSE`.
