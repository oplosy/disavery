# ADR 0003: Build a pinned MinIO release from source

- Status: accepted
- Date: 2026-10-07

## Context
The lab needs S3 Object Lock, versioning and bucket replication; MinIO provides
all three. MinIO's community edition is archived: the `minio/minio` Docker Hub
repository no longer exists and `dl.min.io` answers `410 Gone` for every
community release, including the archive. The source repositories and their
release tags are still available on GitHub (read-only).

## Decision
Build `minio` (`RELEASE.2024-10-13T13-34-11Z`) and `mc`
(`RELEASE.2024-10-08T09-37-26Z`) from their upstream tags in
`images/minio/Dockerfile`. Each tag is checked against a pinned commit SHA, so a
re-pointed tag fails the build. The resulting image `disavery/minio:local` is
used by the integration tests; the toolbox copies both binaries from it, and
Ansible copies them from the toolbox to the nodes.

Alternatives considered: the maintained `pgsty` fork (gets security fixes, but
is a fast-moving third party that was being renamed at the time) and replacing
MinIO with another Object Lock-capable store such as RustFS or Ceph RGW (a
larger design change with unproven compliance-mode behaviour in this lab).

## Consequences
- Reproducible builds with no dependency on MinIO's distribution policy; the
  only external inputs are the Git tags and the Go toolchain image.
- The first build takes a few minutes; Docker's layer cache makes later ones
  instant. CI must build the image before integration tests.
- No security fixes after the pinned date — acceptable for an isolated lab.
- If the GitHub repositories disappear, switch to a fork or another
  Object Lock-capable store; the S3 API surface used here is standard.
