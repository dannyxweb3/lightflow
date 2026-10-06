#!/usr/bin/env python3
"""Provision a country, endpoint and initial account through the admin API."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import secrets
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent

def main():
    p = argparse.ArgumentParser()
    p.add_argument('--email', default='user@example.test')
    p.add_argument('--api', default=None)
    p.add_argument('--days', type=int, default=30)
    p.add_argument('--rotate-gateway-token', action='store_true', help='Replace the token on an existing endpoint and revoke its leases')
    a = p.parse_args()
    env = dict(line.split('=', 1) for line in (ROOT / '.env').read_text().splitlines() if line and not line.startswith('#'))
    base = a.api or 'http://127.0.0.1:' + env.get('API_PORT', '8080')
    def post(path, payload, conflict_ok=False):
        req = urllib.request.Request(base + path, data=json.dumps(payload).encode(), method='POST',
            headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + env['ADMIN_KEY']})
        try:
            with urllib.request.urlopen(req, timeout=15) as res:
                return json.load(res)
        except urllib.error.HTTPError as e:
            if conflict_ok and e.code == 409:
                return None
            raise SystemExit(f'{path}: HTTP {e.code}; {e.read().decode()}') from None
    post('/admin/countries', {'code': env['GATEWAY_COUNTRY'], 'name': env['GATEWAY_COUNTRY']})
    endpoint = post('/admin/endpoints', {'id': env['GATEWAY_ID'], 'country_code': env['GATEWAY_COUNTRY'],
        'host': env['GATEWAY_HOST'], 'port': int(env['GATEWAY_PORT']), 'server_name': env['GATEWAY_HOST'],
        'capacity': 100, 'auth_token': env['GATEWAY_AUTH_TOKEN']}, conflict_ok=True)
    if endpoint is None and a.rotate_gateway_token:
        req=urllib.request.Request(base+'/admin/endpoints/'+env['GATEWAY_ID'],data=json.dumps({'auth_token':env['GATEWAY_AUTH_TOKEN']}).encode(),method='PATCH',headers={'Content-Type':'application/json','Authorization':'Bearer '+env['ADMIN_KEY']})
        try:
            with urllib.request.urlopen(req,timeout=15) as response: response.read()
        except urllib.error.HTTPError as err:
            raise SystemExit(f'gateway token update: HTTP {err.code}; {err.read().decode()}') from None
    password = secrets.token_urlsafe(24)
    user = post('/admin/users', {'email': a.email, 'password': password, 'subscription': {
        'plan': 'standard', 'expires_at': (datetime.now(timezone.utc) + timedelta(days=a.days)).isoformat(),
        'device_limit': 5, 'concurrent_limit': 2, 'enabled': True}}, conflict_ok=True)
    if user:
        os.umask(0o077)
        out = ROOT / '.local' / 'initial-account.json'
        # Avoid overwriting credentials from an earlier bootstrap of a different user.
        if out.exists():
            out = ROOT / '.local' / ('account-' + user['id'] + '.json')
        out.write_text(json.dumps({'id': user['id'], 'email': a.email, 'password': password}, indent=2) + '\n')
        print('Created account; credentials saved to ' + str(out.relative_to(ROOT)))
    else:
        print('Account already exists; password unchanged.')
    print('Country and endpoint are registered. Agent should become available within 5 seconds.')

if __name__ == '__main__':
    main()
