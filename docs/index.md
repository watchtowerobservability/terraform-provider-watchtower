---
page_title: "Provider: Watchtower"
description: |-
  Manage Watchtower configuration as code.
---

# Watchtower Provider

Manages Watchtower configuration through the public `/v1` API.

## Example Usage

```terraform
provider "watchtower" {
  endpoint = "https://api.example.com"
  project  = "prj_..."
}
```

## Schema

### Optional

- `endpoint` (String) API base URL; default `$WT_API`.
- `api_key` (String, Sensitive) API key; default `$WT_API_KEY`.
- `project` (String) Default project id for resources that set none; default `$WT_PROJECT`.
- `org` (String) Default org id for org-scoped resources (ip_allowlist, org_sso, secret_store); default `$WT_ORG`.
