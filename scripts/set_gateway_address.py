#!/usr/bin/env python3
"""Publish a certificate-backed gateway address to an existing control plane."""
import argparse
import ipaddress
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent.parent

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--host', required=True, help='Public gateway DNS name')
    parser.add_argument('--server-name', help='TLS SNI; defaults to --host')
    parser.add_argument('--port', type=int, help='Public UDP port; defaults to GATEWAY_PORT')
    args = parser.parse_args()
    sni = args.server_name or args.host
    for name in (args.host, sni):
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]{0,252}', name) or name.endswith('.') or '..' in name or '.' not in name:
            parser.error('host and server name must be public DNS names')
        try:
            ipaddress.ip_address(name)
        except ValueError:
            pass
        else:
            parser.error('host and server name must be public DNS names, not IP addresses')

    cert = ROOT / '.local/certs/gateway.crt'
    if subprocess.run(['openssl', 'x509', '-in', str(cert), '-noout', '-checkhost', sni], capture_output=True).returncode:
        parser.error('installed gateway certificate does not cover the requested TLS server name')

    env_path = ROOT / '.env'
    raw = env_path.read_text()
    values = dict(line.split('=', 1) for line in raw.splitlines() if '=' in line and not line.startswith('#'))
    endpoint_id = values['GATEWAY_ID']
    port = args.port if args.port is not None else int(values['GATEWAY_PORT'])
    if not 1 <= port <= 65535:
        parser.error('invalid UDP port')
    base = 'http://127.0.0.1:' + values.get('API_PORT', '9010')
    headers = {'Authorization': 'Bearer ' + values['ADMIN_KEY'], 'Content-Type': 'application/json'}

    def current():
        request = urllib.request.Request(base + '/admin/endpoints', headers=headers)
        for attempt in range(6):
            try:
                with urllib.request.urlopen(request, timeout=5) as response:
                    rows = json.load(response)['endpoints']
                return next((row for row in rows if row['id'] == endpoint_id), None)
            except urllib.error.HTTPError as error:
                if error.code < 500:
                    raise SystemExit(f'endpoint lookup failed: HTTP {error.code}') from None
                problem = f'HTTP {error.code}'
            except (urllib.error.URLError, OSError) as error:
                problem = str(error)
            if attempt < 5:
                time.sleep(1)
        raise SystemExit(f'control service unavailable at {base}: {problem}')

    node = current()
    if node is None:
        raise SystemExit(f'gateway {endpoint_id} is not registered; run scripts/bootstrap.py first')
    changed = node['host'] != args.host or node['server_name'] != sni or node['port'] != port
    if changed:
        request = urllib.request.Request(
            base + '/admin/endpoints/' + urllib.parse.quote(endpoint_id, safe=''),
            data=json.dumps({'host': args.host, 'server_name': sni, 'port': port}).encode(),
            headers=headers, method='PATCH')
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                if response.status != 204:
                    raise SystemExit(f'endpoint update failed: HTTP {response.status}')
        except urllib.error.HTTPError as error:
            raise SystemExit(f'endpoint update failed: HTTP {error.code}') from None
        except (urllib.error.URLError, OSError) as error:
            node = current()
            if node is None or node['host'] != args.host or node['server_name'] != sni or node['port'] != port:
                raise SystemExit(f'control connection closed during update ({error}); retry after control is healthy') from None

    replacements = {'GATEWAY_HOST': args.host, 'GATEWAY_SERVER_NAME': sni, 'GATEWAY_PORT': str(port)}
    lines = []
    for line in raw.splitlines():
        key = line.split('=', 1)[0]
        if key in replacements:
            line = key + '=' + replacements.pop(key)
        lines.append(line)
    lines.extend(key + '=' + value for key, value in replacements.items())
    os.umask(0o077)
    with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=ROOT, prefix='.env-gateway-', delete=False) as temp:
        temp.write('\n'.join(lines) + '\n')
        pending = Path(temp.name)
    pending.replace(env_path)

    for _ in range(15):
        node = current()
        if node and node['host'] == args.host and node['server_name'] == sni and node['port'] == port and node['enabled'] and node['ready']:
            print(f'Gateway ready: {args.host}:{port}/udp; SNI: {sni}')
            if changed:
                print('Existing leases on this endpoint were revoked by the address change.')
            return
        time.sleep(1)
    raise SystemExit('Endpoint updated but not ready; inspect gateway logs and health')

if __name__ == '__main__':
    main()
