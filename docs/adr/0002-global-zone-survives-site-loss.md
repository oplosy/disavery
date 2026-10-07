# ADR 0002: The global zone survives site loss

- Status: accepted
- Date: 2026-10-07

## Context
In production, DNS, the CDN/edge and third-party providers are run by other
companies in other failure domains. The lab must still model them.

## Decision
CoreDNS, the Caddy edge and the webhook mock form a "global" zone that drills
never destroy. DNS records and the edge upstream are Terraform-managed (variable
`active_site`), the way Route 53 records would be.

## Consequences
- Site-loss drills measure recovery of the site, not of the internet.
- Repointing traffic is an explicit, timed runbook step (Terraform apply on the
  global zone), and DNS TTL (30 s) is part of measured RTO.
- Failure of the DNS/edge provider itself is out of scope for v1.
