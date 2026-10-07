#!/usr/bin/env python3
"""Generate local secrets and a private PKI. Never overwrites existing state."""
import argparse
import base64
import hashlib
import json
import ipaddress
import os
from pathlib import Path
import re
import secrets
import subprocess

ROOT = Path(__file__).resolve().parent.parent

def run(*args):
    subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)

def main():
    p = argparse.ArgumentParser()
    p.add_argument('--gateway-host', default='localhost')
    p.add_argument('--gateway-id', default='gateway-1')
    p.add_argument('--country', default='SG')
    a = p.parse_args()
    for value in [a.gateway_host]:
        if not re.fullmatch(r'[a-zA-Z0-9.:-]+', value):
            p.error('host must be a DNS name or IP address')
    if not re.fullmatch(r'[a-z0-9][a-z0-9-]{0,62}', a.gateway_id):
        p.error('invalid gateway ID')
    if not re.fullmatch(r'[A-Z]{2}', a.country):
        p.error('country must be two uppercase letters')
    os.umask(0o077)
    if (ROOT / '.env').exists() or (ROOT / '.local').exists():
        p.error('.env or .local already exists; refusing to overwrite keys')
    certs = ROOT / '.local' / 'certs'
    certs.mkdir(parents=True)
    run('openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-nodes', '-sha256',
        '-days', '3650', '-subj', '/CN=Nimbus Internal CA',
        '-keyout', str(certs / 'ca.key'), '-out', str(certs / 'ca.crt'),
        '-addext', 'basicConstraints=critical,CA:TRUE',
        '-addext', 'keyUsage=critical,keyCertSign,cRLSign')
    def issue(name, cn, hosts=(), client=False):
        run('openssl', 'req', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN=' + cn,
            '-keyout', str(certs / (name + '.key')), '-out', str(certs / (name + '.csr')))
        ext = ['basicConstraints=critical,CA:FALSE', 'keyUsage=critical,digitalSignature,keyEncipherment',
               'extendedKeyUsage=' + ('clientAuth' if client else 'serverAuth')]
        san = []
        for host in dict.fromkeys(hosts):
            try:
                ipaddress.ip_address(host)
                san.append('IP:' + host)
            except ValueError:
                san.append('DNS:' + host)
        if san:
            ext.append('subjectAltName=' + ','.join(san))
        extfile = certs / (name + '.ext')
        extfile.write_text('\n'.join(ext) + '\n')
        run('openssl', 'x509', '-req', '-in', str(certs / (name + '.csr')),
            '-CA', str(certs / 'ca.crt'), '-CAkey', str(certs / 'ca.key'),
            '-CAcreateserial', '-days', '365', '-sha256', '-extfile', str(extfile),
            '-out', str(certs / (name + '.crt')))
        (certs / (name + '.csr')).unlink()
        extfile.unlink()
    issue('gateway', a.gateway_host, [a.gateway_host, 'localhost', '127.0.0.1'])
    seed = secrets.token_bytes(32)
    # RFC 8410 Ed25519 PKCS#8 DER prefix followed by the 32-byte seed.
    seedfile = ROOT / '.local' / 'signing.der'
    seedfile.write_bytes(bytes.fromhex('302e020100300506032b657004220420') + seed)
    public = subprocess.check_output(['openssl', 'pkey', '-inform', 'DER', '-in', str(seedfile), '-pubout', '-outform', 'DER'])
    (ROOT / '.local' / 'signing-public-key.txt').write_text(base64.b64encode(public[-32:]).decode() + '\n')
    seedfile.unlink()
    admin_password = secrets.token_urlsafe(24)
    admin_salt = secrets.token_bytes(16)
    admin_hash = hashlib.pbkdf2_hmac('sha256', admin_password.encode(), admin_salt, 600000, 32)
    (ROOT / '.local' / 'admin-console.json').write_text(json.dumps({'password': admin_password}) + '\n')
    settings = {
        'POSTGRES_PASSWORD': secrets.token_hex(24), 'ADMIN_KEY': secrets.token_urlsafe(36),
        'ADMIN_CONSOLE_PASSWORD_HASH': "'pbkdf2-sha256$600000$" + base64.b64encode(admin_salt).decode().rstrip('=') + '$' + base64.b64encode(admin_hash).decode().rstrip('=') + "'",
        'ADMIN_COOKIE_SECURE': 'true',
        'CREDENTIAL_KEY': base64.b64encode(secrets.token_bytes(32)).decode(),
        'SIGNING_KEY': base64.b64encode(seed).decode(),
        'LOCAL_UID': str(os.getuid()), 'LOCAL_GID': str(os.getgid()),
        'API_PORT': '8080', 'LEASE_SECONDS': '600',
        'GATEWAY_AUTH_TOKEN': secrets.token_urlsafe(48),
        'GATEWAY_ID': a.gateway_id, 'GATEWAY_HOST': a.gateway_host,
        'GATEWAY_SERVER_NAME': a.gateway_host,
        'GATEWAY_PORT': '4433', 'GATEWAY_COUNTRY': a.country,
    }
    (ROOT / '.env').write_text(''.join(f'{k}={v}\n' for k, v in settings.items()))
    print('Created .env and .local/certs (private keys, mode 0600).')
    print('Next: docker compose up -d --build postgres control && python3 scripts/bootstrap.py && docker compose up -d --build gateway')
    print('Gateway certificate uses the generated private CA; replace it for public production clients.')

if __name__ == '__main__':
    main()
