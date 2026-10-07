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

def endpoints(base, headers):
    request = urllib.request.Request(base + '/admin/endpoints', headers=headers)
    for attempt in range(6):
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                return json.load(response)['endpoints']
        except urllib.error.HTTPError as error:
            if error.code < 500:
                raise SystemExit(f'endpoint lookup failed: HTTP {error.code}') from None
            problem = f'HTTP {error.code}'
        except (urllib.error.URLError, OSError) as error:
            problem = str(error)
        if attempt < 5:
            time.sleep(1)
    raise SystemExit(f'control service unavailable at {base}: {problem}')

def current_endpoint(base, headers, endpoint_id):
    return next((item for item in endpoints(base, headers) if item['id'] == endpoint_id), None)

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
    endpoint = current_endpoint(base, headers, endpoint_id)
    if endpoint is None:
        raise SystemExit(f'gateway {endpoint_id} is not registered; run scripts/bootstrap.py first')
    changed = endpoint['host'] != args.ip or endpoint.get('server_name') != sni or endpoint['port'] != port
    if changed:
        path = '/admin/endpoints/' + urllib.parse.quote(endpoint_id, safe='')
        request = urllib.request.Request(base + path,
            data=json.dumps({'host': args.ip, 'server_name': sni, 'port': port}).encode(),
            headers=headers, method='PATCH')
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                if response.status != 204:
                    raise SystemExit(f'endpoint update failed: HTTP {response.status}')
        except urllib.error.HTTPError as error:
            raise SystemExit(f'endpoint update failed: HTTP {error.code}; deploy the updated control service first') from None
        except (urllib.error.URLError, OSError) as error:
            # The control container may restart after committing the PATCH. Check
            # the result before suggesting another run, which would revoke leases.
            endpoint = current_endpoint(base, headers, endpoint_id)
            if endpoint is None or endpoint['host'] != args.ip or endpoint.get('server_name') != sni or endpoint['port'] != port:
                raise SystemExit(f'control connection closed during endpoint update ({error}); retry after control is healthy') from None
    lines = [line for line in raw.splitlines() if not line.startswith(('GATEWAY_HOST=', 'GATEWAY_SERVER_NAME='))]
    lines.extend(['GATEWAY_HOST=' + args.ip, 'GATEWAY_SERVER_NAME=' + sni])
    os.umask(0o077)
    with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=ROOT, prefix='.env-lan-', delete=False) as temp:
        temp.write('\n'.join(lines) + '\n')
        pending = Path(temp.name)
    pending.replace(env_path)
    ready = False
    for _ in range(12):
        endpoint = current_endpoint(base, headers, endpoint_id)
        if endpoint and endpoint['host'] == args.ip and endpoint['server_name'] == sni and endpoint['port'] == port:
            ready = bool(endpoint['ready']) and bool(endpoint['enabled'])
            if ready:
                break
        time.sleep(1)
    print(f'Gateway candidate: {args.ip}:{port} (UDP), SNI: {sni}, ready: {ready}')
    if not ready:
        raise SystemExit('Endpoint updated but not ready yet; inspect gateway status before connecting')
    if changed:
        print('Existing leases on this endpoint were revoked by the address change.')

if __name__ == '__main__':
    main()
