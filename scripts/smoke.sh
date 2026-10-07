#!/usr/bin/env bash
# End-to-end smoke test of the local lab. Runs inside the toolbox.
set -euo pipefail

SECRETS=/secrets/local.sops.yaml
DNS_SERVER=172.31.0.10
HOST=docs.disavery.test
BASE="https://$HOST"
SSH=(ssh -i /secrets/ssh/id_ed25519 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)

step() { printf '\n==> %s\n' "$*"; }
fail() { printf 'SMOKE FAIL: %s\n' "$*" >&2; exit 1; }
secret() { sops -d --extract "$1" "$SECRETS"; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
secret '["tls"]["ca_crt"]' > "$work/ca.crt"
mkdir -p ~/.mc/certs/CAs && cp "$work/ca.crt" ~/.mc/certs/CAs/disavery-ca.crt

step "DNS resolves $HOST through CoreDNS"
edge_ip=$(dig +short @"$DNS_SERVER" "$HOST")
[[ -n $edge_ip ]] || fail "$HOST did not resolve"
CURL=(curl -fsS --cacert "$work/ca.crt" --resolve "$HOST:443:$edge_ip")

step "application is ready behind the edge (TLS verified)"
"${CURL[@]}" "$BASE/readyz" >/dev/null || fail "readyz failed"

step "upload and download a document"
echo "smoke $(date -u +%FT%TZ)" > "$work/doc.txt"
id=$("${CURL[@]}" -F title=smoke -F "file=@$work/doc.txt" "$BASE/documents" | jq -r .id)
[[ $id =~ ^[0-9a-f-]{36}$ ]] || fail "unexpected document id: $id"
"${CURL[@]}" "$BASE/documents/$id/attachment" | cmp - "$work/doc.txt" || fail "attachment mismatch"

step "payment webhook round trip (allowlist, DNS, TLS, HMAC)"
status=pending
for _ in $(seq 1 20); do
  status=$("${CURL[@]}" "$BASE/documents/$id" | jq -r .payment_status)
  [[ $status == paid ]] && break
  sleep 1
done
[[ $status == paid ]] || fail "payment status stayed '$status'"

step "WAL archiving reaches both repositories"
"${SSH[@]}" root@db-a runuser -u postgres -- pgbackrest --stanza=main check >/dev/null || fail "pgbackrest check failed"

step "a backup exists in both repositories"
repos=$("${SSH[@]}" root@db-a runuser -u postgres -- pgbackrest --stanza=main info --output=json \
  | jq '[.[0].backup[].database["repo-key"]] | unique | length')
[[ $repos == 2 ]] || fail "expected backups in 2 repositories, found $repos"

step "attachment replicated to the vault"
mc alias set vaultw https://vault:9000 "$(secret '["minio"]["vault_writer_access_key"]')" "$(secret '["minio"]["vault_writer_secret_key"]')" >/dev/null
mc alias set vaultroot https://vault:9000 "$(secret '["minio"]["vault_root_user"]')" "$(secret '["minio"]["vault_root_password"]')" >/dev/null
for _ in $(seq 1 30); do
  mc stat "vaultw/attachments/documents/$id" >/dev/null 2>&1 && break
  sleep 1
done
version=$(mc stat --json "vaultw/attachments/documents/$id" | jq -r .versionID)
[[ -n $version && $version != null ]] || fail "attachment was not replicated to the vault"

step "replica is locked in compliance mode by the sweeper"
mode=""
for _ in $(seq 1 90); do
  mode=$(mc retention info --json --version-id "$version" "vaultroot/attachments/documents/$id" | jq -r .mode)
  [[ $mode == COMPLIANCE ]] && break
  sleep 1
done
[[ $mode == COMPLIANCE ]] || fail "replica still has retention mode '$mode'"

step "production credentials cannot destroy vault versions"
if mc rm --version-id "$version" "vaultw/attachments/documents/$id" >/dev/null 2>&1; then
  fail "production writer deleted a vault version"
fi

step "even the vault root cannot delete a locked version"
if mc rm --version-id "$version" "vaultroot/attachments/documents/$id" >/dev/null 2>&1; then
  fail "a locked version was deleted"
fi

step "canary writes are acknowledged and journaled"
last=$(tail -n 1 /state/canary/journal.jsonl 2>/dev/null | jq -r .acked)
[[ -n $last && $last != null ]] || fail "canary journal is empty; is the canary service running?"
age=$(( $(date +%s) - $(date -d "$last" +%s) ))
(( age <= 10 )) || fail "last canary write was acknowledged ${age}s ago"

printf '\nSMOKE PASS\n'
