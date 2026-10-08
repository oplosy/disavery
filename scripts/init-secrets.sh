#!/usr/bin/env bash
# Generates every local secret once and stores it SOPS-encrypted in /secrets.
# The age key is copied to /escrow, a separate volume used for secret-loss drills (S5).
# Usage (inside the toolbox): bash scripts/init-secrets.sh [--force]
set -euo pipefail

SECRETS=/secrets
ESCROW=/escrow
OUT=$SECRETS/local.sops.yaml

rand() { python3 -c 'import secrets; print(secrets.token_hex(24))'; }

if [[ -f $OUT && ${1:-} != --force ]]; then
  # Secrets introduced after the file was generated are added in place, so an
  # existing lab gets them without regenerating (and losing) the others.
  add() {
    if ! sops --decrypt --extract "$1" "$OUT" >/dev/null 2>&1; then
      sops set "$OUT" "$1" "\"$2\""
      echo "added $1"
    fi
  }
  add '["minio"]["vault_reader_access_key"]' vaultreader
  add '["minio"]["vault_reader_secret_key"]' "$(rand)"
  echo "secrets already exist at $OUT (use --force to regenerate)"
  exit 0
fi

umask 077
rm -rf "$SECRETS/age" "$SECRETS/ssh"
mkdir -p "$SECRETS/age" "$SECRETS/ssh" "$ESCROW"

age-keygen -o "$SECRETS/age/keys.txt" 2>/dev/null
cp "$SECRETS/age/keys.txt" "$ESCROW/age-keys.txt"
ssh-keygen -q -t ed25519 -N '' -C disavery -f "$SECRETS/ssh/id_ed25519"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

step certificate create "disavery local CA" "$tmp/ca.crt" "$tmp/ca.key" \
  --profile root-ca --no-password --insecure --not-after 87600h
step certificate create docs.disavery.test "$tmp/docs.crt" "$tmp/docs.key" \
  --profile leaf --ca "$tmp/ca.crt" --ca-key "$tmp/ca.key" --no-password --insecure \
  --not-after 8760h --san docs.disavery.test --san direct.docs.disavery.test
step certificate create vault "$tmp/vault.crt" "$tmp/vault.key" \
  --profile leaf --ca "$tmp/ca.crt" --ca-key "$tmp/ca.key" --no-password --insecure \
  --not-after 8760h --san vault

block() { sed 's/^/    /' "$1"; }

cat > "$tmp/plain.yaml" <<EOF
postgres:
  app_password: $(rand)
  replication_password: $(rand)
minio:
  site_root_user: siteadmin
  site_root_password: $(rand)
  app_access_key: docsvc
  app_secret_key: $(rand)
  vault_root_user: vaultadmin
  vault_root_password: $(rand)
  vault_writer_access_key: prodwriter
  vault_writer_secret_key: $(rand)
  vault_reader_access_key: vaultreader
  vault_reader_secret_key: $(rand)
pgbackrest:
  repo2_cipher_pass: $(rand)
webhook:
  hmac_secret: $(rand)
tls:
  ca_crt: |
$(block "$tmp/ca.crt")
  ca_key: |
$(block "$tmp/ca.key")
  docs_crt: |
$(block "$tmp/docs.crt")
  docs_key: |
$(block "$tmp/docs.key")
  vault_crt: |
$(block "$tmp/vault.crt")
  vault_key: |
$(block "$tmp/vault.key")
EOF

sops --encrypt --age "$(age-keygen -y "$SECRETS/age/keys.txt")" \
  --input-type yaml --output-type yaml "$tmp/plain.yaml" > "$OUT"

echo "wrote $OUT; age key escrowed to $ESCROW/age-keys.txt"
