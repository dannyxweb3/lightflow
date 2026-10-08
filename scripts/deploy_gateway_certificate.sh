#!/bin/sh
set -eu

script=$(readlink -f -- "$0")
root=$(dirname -- "$(dirname -- "$script")")
cd "$root"

domain=${GATEWAY_CERT_DOMAIN:-}
if [ -z "$domain" ]; then
  domain=$(sed -n 's/^GATEWAY_CERT_DOMAIN=//p' .env | tail -n 1)
fi
if [ -z "$domain" ]; then
  domain=$(sed -n 's/^GATEWAY_HOST=//p' .env | tail -n 1)
fi
case "$domain" in
  ''|localhost|127.*|*[!A-Za-z0-9.-]*|*..*|.*|*.)
    echo 'Set GATEWAY_CERT_DOMAIN to the public gateway DNS name.' >&2
    exit 2 ;;
esac
if [ -n "${RENEWED_DOMAINS:-}" ]; then
  case " ${RENEWED_DOMAINS} " in
    *" ${domain} "*) ;;
    *) exit 0 ;;
  esac
fi

if [ "$#" -eq 2 ]; then
  cert=$1
  key=$2
elif [ "$#" -le 1 ]; then
  lineage=${1:-${RENEWED_LINEAGE:-}}
  if [ -z "$lineage" ]; then
    echo 'Usage: deploy_gateway_certificate.sh CERT KEY | CERTBOT_LINEAGE' >&2
    exit 2
  fi
  cert=$lineage/fullchain.pem
  key=$lineage/privkey.pem
else
  echo 'Usage: deploy_gateway_certificate.sh CERT KEY | CERTBOT_LINEAGE' >&2
  exit 2
fi

if ! openssl x509 -in "$cert" -noout -checkhost "$domain" >/dev/null; then
  echo "Gateway certificate does not cover $domain." >&2
  exit 1
fi
public_cert=$(mktemp)
public_key=$(mktemp)
trap 'rm -f "$public_cert" "$public_key"' EXIT HUP INT TERM
openssl x509 -in "$cert" -pubkey -noout -out "$public_cert"
openssl pkey -in "$key" -pubout -out "$public_key"
if ! cmp -s "$public_cert" "$public_key"; then
  echo 'Gateway certificate and private key do not match.' >&2
  exit 1
fi

uid=$(sed -n 's/^LOCAL_UID=//p' .env | tail -n 1)
gid=$(sed -n 's/^LOCAL_GID=//p' .env | tail -n 1)
uid=${uid:-10001}
gid=${gid:-10001}
install -m 0644 "$cert" .local/certs/gateway.crt
install -o "$uid" -g "$gid" -m 0600 "$key" .local/certs/gateway.key
docker compose up -d --no-deps --force-recreate --wait gateway
echo "Gateway now serves a certificate for $domain."
