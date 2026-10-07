# ADR 0001: Systemd containers as lab "machines"

- Status: accepted
- Date: 2026-10-07

## Context
The lab needs machines that Terraform can create and destroy quickly and that
Ansible can configure over SSH, so the same roles later run on cloud VMs.
Real VMs (Hyper-V, Vagrant) take minutes per rebuild and are hard to run in CI.

## Decision
Each machine is a privileged Debian 12 container running systemd as PID 1 with
sshd, created by Terraform's Docker provider and configured by Ansible.

## Consequences
- A site rebuilds in under a minute and the whole lab runs on a GitHub runner.
- Ansible roles are VM-ready; the cloud profile only swaps the Terraform provider.
- Containers share the host kernel and use overlay storage, so fsync/disk
  latency, kernel tunables and failure modes differ from VMs. Measured RTOs are
  lab numbers, not production predictions.
- Containers run privileged; acceptable for a local lab only.
