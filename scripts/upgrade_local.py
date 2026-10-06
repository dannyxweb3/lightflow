#!/usr/bin/env python3
"""Add a per-gateway control token to an existing private .env once."""
from pathlib import Path
import os
import secrets

root=Path(__file__).resolve().parent.parent
path=root/'.env'
if not path.exists():
    raise SystemExit('No .env found. Run scripts/init.py for a new deployment.')
text=path.read_text()
if any(line.startswith('GATEWAY_AUTH_TOKEN=') and line.split('=',1)[1] for line in text.splitlines()):
    print('Gateway token already exists; no changes made.')
else:
    os.umask(0o077)
    with path.open('a') as f:
        f.write(('' if text.endswith('\n') else '\n')+'GATEWAY_AUTH_TOKEN='+secrets.token_urlsafe(48)+'\n')
    print('Added GATEWAY_AUTH_TOKEN to .env. Recreate the gateway container after bootstrap.')
