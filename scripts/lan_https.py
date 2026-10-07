#!/usr/bin/env python3
"""Serve the public /v1 API over LAN HTTPS using an unprivileged Nginx process."""
import argparse
import ipaddress
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
CERTS = ROOT / '.local' / 'certs'
RUNTIME = ROOT / '.local' / 'lan-nginx'

def command(*args):
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f'{args[0]} failed: {result.stderr[-1200:]}')

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--ip', required=True, help='VM LAN IPv4 address reachable from Windows')
    parser.add_argument('--port', type=int, default=8443)
    parser.add_argument('--api-port', type=int, default=9010)
    args = parser.parse_args()
    ipaddress.IPv4Address(args.ip)
    if not (1024 <= args.port <= 65535 and 1 <= args.api_port <= 65535):
        parser.error('invalid port')
    nginx = shutil.which('nginx')
    if not nginx:
        parser.error('nginx executable not found')
    if not (CERTS / 'ca.crt').is_file() or not (CERTS / 'ca.key').is_file():
        parser.error('CA files missing; run scripts/init.py first')
    version = subprocess.run([nginx, '-V'], capture_output=True, text=True)
    lua_directives = ''
    if 'lua_nginx_module' in version.stderr:
        lua_roots = (Path('/www/server/nginx/lib/lua'), Path('/usr/local/openresty/lualib'))
        lua_root = next((path for path in lua_roots if (path / 'resty/core.lua').is_file()), None)
        if lua_root is None:
            parser.error('this Lua-enabled Nginx lacks resty.core; install the matching Lua library')
        lua_directives = f'  lua_package_path "{lua_root}/?.lua;;";\n  lua_package_cpath "{lua_root}/?.so;;";\n'
    os.umask(0o077)
    RUNTIME.mkdir(parents=True, exist_ok=True)
    (RUNTIME / 'logs').mkdir(exist_ok=True)
    cert = CERTS / 'lan-api.crt'
    key = CERTS / 'lan-api.key'
    with tempfile.TemporaryDirectory(prefix='lan-cert-', dir=CERTS) as temp:
        temp = Path(temp)
        request, new_key, new_cert, extensions = (temp / name for name in ('api.csr', 'api.key', 'api.crt', 'api.ext'))
        extensions.write_text('basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=IP:' + args.ip + '\n')
        command('openssl', 'req', '-new', '-newkey', 'rsa:3072', '-nodes', '-sha256',
                '-subj', '/CN=' + args.ip, '-keyout', str(new_key), '-out', str(request))
        command('openssl', 'x509', '-req', '-in', str(request), '-CA', str(CERTS / 'ca.crt'),
                '-CAkey', str(CERTS / 'ca.key'), '-set_serial', '0x' + secrets.token_hex(16),
                '-days', '365', '-sha256', '-extfile', str(extensions), '-out', str(new_cert))
        new_key.replace(key)
        new_cert.replace(cert)
    config = RUNTIME / 'nginx.conf'
    config.write_text(f'''worker_processes 1;
pid "{RUNTIME / 'nginx.pid'}";
error_log "{RUNTIME / 'error.log'}" notice;
events {{ worker_connections 256; }}
http {{
{lua_directives}
  access_log "{RUNTIME / 'access.log'}";
  client_body_temp_path "{RUNTIME / 'client_body'}";
  proxy_temp_path "{RUNTIME / 'proxy_temp'}";
  server {{
    listen {args.ip}:{args.port} ssl;
    server_name {args.ip};
    server_tokens off;
    ssl_certificate "{cert}";
    ssl_certificate_key "{key}";
    ssl_protocols TLSv1.2 TLSv1.3;
    client_max_body_size 128k;
    location = /readyz {{ proxy_pass http://127.0.0.1:{args.api_port}; }}
    location ^~ /v1/ {{
      proxy_pass http://127.0.0.1:{args.api_port};
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-Proto https;
      proxy_read_timeout 15s;
    }}
    location / {{ return 404; }}
  }}
}}
''')
    command(nginx, '-p', str(RUNTIME) + '/', '-c', str(config), '-t')
    pid = RUNTIME / 'nginx.pid'
    if pid.exists() and pid.read_text().strip():
        try:
            os.kill(int(pid.read_text().strip()), 0)
            command(nginx, '-p', str(RUNTIME) + '/', '-c', str(config), '-s', 'reload')
        except (OSError, ValueError):
            command(nginx, '-p', str(RUNTIME) + '/', '-c', str(config))
    else:
        command(nginx, '-p', str(RUNTIME) + '/', '-c', str(config))
    print(f'LAN API: https://{args.ip}:{args.port}')
    print('Public paths: /v1/* and /readyz. Admin and internal paths return 404.')
    print('This process must be restarted after a VM reboot; certificate expires in one year.')

if __name__ == '__main__':
    main()
