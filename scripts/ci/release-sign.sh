#!/usr/bin/env bash
# OPS69: sign the release manifest with Ed25519 (the update trust, owner decision 21 Nov).
#   CRSP_UPDATE_SIGNING_KEY=<PKCS#8 PEM> bash scripts/ci/release-sign.sh dist/manifest.json dist/manifest.json.sig
# The signature is Ed25519 over the manifest file's bytes (openssl pkeyutl -sign -rawin), written as hex.
# A missing key fails. The signature is checked with the public key derived from the private key and, when
# internal/update/keys/release-ed25519.pub is committed, with that file too (a key that does not match fails).
set -euo pipefail
manifest="${1:?manifest path}"
sigout="${2:?signature path}"
pubfile="${CRSP_UPDATE_PUBLIC_KEY_FILE:-internal/update/keys/release-ed25519.pub}"

if [ -z "${CRSP_UPDATE_SIGNING_KEY:-}" ]; then
  echo "::error::the secret CRSP_UPDATE_SIGNING_KEY is missing; the release cannot be signed" >&2
  exit 1
fi
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
umask 077
printf '%s\n' "$CRSP_UPDATE_SIGNING_KEY" > "$work/key.pem"
openssl pkey -in "$work/key.pem" -noout 2>/dev/null || { echo "::error::CRSP_UPDATE_SIGNING_KEY is not a PEM private key" >&2; exit 1; }
[ "$(openssl pkey -in "$work/key.pem" -text -noout | head -1)" = "ED25519 Private-Key:" ] \
  || { echo "::error::CRSP_UPDATE_SIGNING_KEY is not an Ed25519 key" >&2; exit 1; }

openssl pkeyutl -sign -rawin -inkey "$work/key.pem" -in "$manifest" -out "$work/sig.bin"
openssl pkey -in "$work/key.pem" -pubout -out "$work/derived.pub"
openssl pkeyutl -verify -rawin -pubin -inkey "$work/derived.pub" -in "$manifest" -sigfile "$work/sig.bin" >/dev/null
if [ -f "$pubfile" ]; then
  openssl pkeyutl -verify -rawin -pubin -inkey "$pubfile" -in "$manifest" -sigfile "$work/sig.bin" >/dev/null \
    || { echo "::error::the signature does not verify with $pubfile; the secret is not the key the app trusts" >&2; exit 1; }
fi
python3 -c 'import sys; sys.stdout.write(open(sys.argv[1], "rb").read().hex())' "$work/sig.bin" > "$sigout"
echo "signed $manifest ($(wc -c < "$sigout") hex characters)"
