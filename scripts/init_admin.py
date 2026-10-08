#!/usr/bin/env python3
"""Add one console operator password to an existing installation."""
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets

root = Path(__file__).resolve().parent.parent
env = root / '.env'
if not env.exists():
    raise SystemExit('No .env found; run scripts/init.py for a new deployment.')
lines = env.read_text()
if any(line.startswith('ADMIN_CONSOLE_PASSWORD_HASH=') and line.split('=', 1)[1] for line in lines.splitlines()):
    print('Console password already configured; no changes made.')
    raise SystemExit(0)
os.umask(0o077)
password = secrets.token_urlsafe(24)
salt = secrets.token_bytes(16)
digest = hashlib.pbkdf2_hmac('sha256', password.encode(), salt, 600000, 32)
encoded = 'pbkdf2-sha256$600000$' + base64.b64encode(salt).decode().rstrip('=') + '$' + base64.b64encode(digest).decode().rstrip('=')
with env.open('a') as f:
    f.write(('' if lines.endswith('\n') else '\n') + "ADMIN_CONSOLE_PASSWORD_HASH='" + encoded + "'\n")
    if not any(line.startswith('ADMIN_COOKIE_SECURE=') for line in lines.splitlines()):
        f.write('ADMIN_COOKIE_SECURE=true\n')
credentials = root / '.local' / 'admin-console.json'
credentials.write_text(json.dumps({'password': password}) + '\n')
print('Console password added. Read .local/admin-console.json and recreate the control container.')
