#!/usr/bin/env python3
"""Move an existing deployment's API host port from 8080 to 9010."""
import os
from pathlib import Path
import tempfile

root = Path(__file__).resolve().parent.parent
env_path = root / '.env'
if not env_path.is_file():
    raise SystemExit('No .env found; run scripts/init.py for a new deployment.')
raw = env_path.read_text()
lines = raw.splitlines()
ports = [line.split('=', 1)[1] for line in lines if line.startswith('API_PORT=')]
if len(ports) > 1:
    raise SystemExit('Multiple API_PORT entries in .env; resolve them manually.')
if ports and ports[0] not in ('8080', '9010'):
    raise SystemExit(f'Custom API_PORT={ports[0]}; choose a port in 9010–9020 manually.')
if ports == ['9010']:
    print('API_PORT is already 9010; no changes made.')
    raise SystemExit(0)
if ports:
    lines = ['API_PORT=9010' if line.startswith('API_PORT=') else line for line in lines]
else:
    lines.append('API_PORT=9010')
os.umask(0o077)
with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=root, prefix='.env-ports-', delete=False) as temp:
    temp.write('\n'.join(lines) + '\n')
    pending = Path(temp.name)
pending.replace(env_path)
print('Set API_PORT=9010 in .env. Recreate postgres, control, and gateway together.')
