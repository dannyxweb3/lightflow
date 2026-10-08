#!/bin/sh
set -eu

domain=lightflow-gw.aibusinesses.cc
if [ -n "${RENEWED_DOMAINS:-}" ]; then
  case " ${RENEWED_DOMAINS} " in
    *" ${domain} "*) ;;
    *) exit 0 ;;
  esac
fi

lineage=${1:-${RENEWED_LINEAGE:-}}
if [ -z "$lineage" ]; then
  echo 'Usage: deploy_gateway_certificate.sh /etc/letsencrypt/live/lightflow-gw.aibusinesses.cc' >&2
  exit 2
fi

script=$(readlink -f -- "$0")
root=$(dirname -- "$(dirname -- "$script")")
cd "$root"

openssl x509 -in "$lineage/fullchain.pem" -noout -checkhost "$domain" >/dev/null
public_cert=$(mktemp)
public_key=$(mktemp)
trap 'rm -f "$public_cert" "$public_key"' EXIT HUP INT TERM
openssl x509 -in "$lineage/fullchain.pem" -pubkey -noout -out "$public_cert"
openssl pkey -in "$lineage/privkey.pem" -pubout -out "$public_key"
if ! cmp -s "$public_cert" "$public_key"; then
  echo 'Gateway certificate and private key do not match.' >&2
  exit 1
fi

uid=$(sed -n 's/^LOCAL_UID=//p' .env | tail -n 1)
gid=$(sed -n 's/^LOCAL_GID=//p' .env | tail -n 1)
uid=${uid:-10001}
gid=${gid:-10001}
install -m 0644 "$lineage/fullchain.pem" .local/certs/gateway.crt
install -o "$uid" -g "$gid" -m 0600 "$lineage/privkey.pem" .local/certs/gateway.key
docker compose up -d --no-deps --force-recreate --wait gateway
echo "Gateway now serves a certificate for $domain."
