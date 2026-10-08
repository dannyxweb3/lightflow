CREATE TABLE admin_sessions (
 token_hash text PRIMARY KEY,
 csrf_hash text NOT NULL,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_sessions_expires ON admin_sessions(expires_at);
