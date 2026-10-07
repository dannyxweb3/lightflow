#!/usr/bin/env python3
"""Update the registered gateway candidate for LAN clients without exposing secrets."""
import argparse
import ipaddress
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent.parent

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--ip', required=True, help='VM LAN IPv4 address reachable from Windows')
    args = parser.parse_args()
    ipaddress.IPv4Address(args.ip)
    env_path = ROOT / '.env'
    raw = env_path.read_text()
    values = dict(line.split('=', 1) for line in raw.splitlines() if '=' in line and not line.startswith('#'))
    sni = values.get('GATEWAY_SERVER_NAME', 'localhost')
    check = subprocess.run(['openssl', 'x509', '-in', str(ROOT / '.local/certs/gateway.crt'),
                            '-noout', '-checkhost', sni], capture_output=True)
    if check.returncode:
        parser.error('gateway certificate does not cover the configured server name')
    endpoint_id = values['GATEWAY_ID']
    port = int(values['GATEWAY_PORT'])
    base = 'http://127.0.0.1:' + values.get('API_PORT', '8080')
    headers = {'Authorization': 'Bearer ' + values['ADMIN_KEY'], 'Content-Type': 'application/json'}
    path = '/admin/endpoints/' + urllib.parse.quote(endpoint_id, safe='')
    request = urllib.request.Request(base + path,
        data=json.dumps({'host': args.ip, 'server_name': sni}).encode(),
        headers=headers, method='PATCH')
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            if response.status != 204:
                raise RuntimeError('unexpected endpoint update response')
    except urllib.error.HTTPError as error:
        raise SystemExit(f'endpoint update failed: HTTP {error.code}; deploy the updated control service first') from None
    lines = [line for line in raw.splitlines() if not line.startswith(('GATEWAY_HOST=', 'GATEWAY_SERVER_NAME='))]
    lines.extend(['GATEWAY_HOST=' + args.ip, 'GATEWAY_SERVER_NAME=' + sni])
    os.umask(0o077)
    with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=ROOT, prefix='.env-lan-', delete=False) as temp:
        temp.write('\n'.join(lines) + '\n')
        pending = Path(temp.name)
    pending.replace(env_path)
    ready = False
    for _ in range(12):
        status = urllib.request.Request(base + '/admin/endpoints', headers=headers)
        with urllib.request.urlopen(status, timeout=5) as response:
            endpoints = json.load(response)['endpoints']
        endpoint = next((item for item in endpoints if item['id'] == endpoint_id), None)
        if endpoint and endpoint['host'] == args.ip and endpoint['server_name'] == sni and endpoint['port'] == port:
            ready = bool(endpoint['ready']) and bool(endpoint['enabled'])
            if ready:
                break
        time.sleep(1)
    print(f'Gateway candidate: {args.ip}:{port} (UDP), SNI: {sni}, ready: {ready}')
    if not ready:
        raise SystemExit('Endpoint updated but not ready yet; inspect gateway status before connecting')
    print('Existing leases on this endpoint were revoked by the address change.')

if __name__ == '__main__':
    main()
