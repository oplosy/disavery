#!/usr/bin/env bash
# Runs a command on a lab node as root with the lab SSH key, for runbook steps
# that pipe data from one node to another (e.g. a table dump in S2).
# Usage (inside the toolbox): scripts/lab-ssh.sh HOST COMMAND...
set -euo pipefail

host=${1:?usage: lab-ssh.sh HOST COMMAND...}
shift
exec ssh -i /secrets/ssh/id_ed25519 \
  -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  -o LogLevel=ERROR -o BatchMode=yes -o ConnectTimeout=10 \
  "root@$host" "$@"
