CREATE TABLE IF NOT EXISTS users (
 id text PRIMARY KEY, email text NOT NULL UNIQUE, password_hash text NOT NULL,
 enabled boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS subscriptions (
 user_id text PRIMARY KEY REFERENCES users(id), plan text NOT NULL,
 expires_at timestamptz NOT NULL, device_limit integer NOT NULL CHECK(device_limit BETWEEN 1 AND 100),
 concurrent_limit integer NOT NULL CHECK(concurrent_limit BETWEEN 1 AND 100), enabled boolean NOT NULL DEFAULT true
);
CREATE TABLE IF NOT EXISTS tokens (
 hash text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), family text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('access','refresh')), expires_at timestamptz NOT NULL,
 consumed boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tokens_user ON tokens(user_id);
CREATE TABLE IF NOT EXISTS devices (
 id text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), name text NOT NULL,
 os text NOT NULL CHECK(os IN ('windows','macos','linux')), public_key text NOT NULL,
 revoked_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(user_id,public_key)
);
CREATE TABLE IF NOT EXISTS countries(code text PRIMARY KEY, name text NOT NULL, enabled boolean NOT NULL DEFAULT true);
CREATE TABLE IF NOT EXISTS endpoints (
 id text PRIMARY KEY, country_code text NOT NULL REFERENCES countries(code), host text NOT NULL,
 port integer NOT NULL CHECK(port BETWEEN 1 AND 65535), server_name text NOT NULL,
 protocol text NOT NULL DEFAULT 'hysteria2' CHECK(protocol='hysteria2'),
 capacity integer NOT NULL CHECK(capacity BETWEEN 1 AND 5000), enabled boolean NOT NULL DEFAULT true,
 last_seen_at timestamptz, ready boolean NOT NULL DEFAULT false
);
CREATE TABLE IF NOT EXISTS sessions (
 id text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), device_id text NOT NULL REFERENCES devices(id),
 endpoint_id text NOT NULL REFERENCES endpoints(id), state text NOT NULL CHECK(state IN ('pending','issued','active','released','revoked','expired')),
 expires_at timestamptz NOT NULL, confirmed_until timestamptz, credential_hash text NOT NULL,
 idempotency_key text NOT NULL, request_hash text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(user_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS sessions_quota ON sessions(user_id,expires_at) WHERE state IN ('pending','issued','active');
CREATE INDEX IF NOT EXISTS sessions_endpoint ON sessions(endpoint_id,expires_at);
CREATE TABLE IF NOT EXISTS proof_nonces(device_id text NOT NULL REFERENCES devices(id), nonce text NOT NULL, expires_at timestamptz NOT NULL, PRIMARY KEY(device_id,nonce));
CREATE TABLE IF NOT EXISTS documents (
 kind text NOT NULL CHECK(kind IN ('policy','manifest')), version bigint NOT NULL,
 payload jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(kind,version)
);
CREATE TABLE IF NOT EXISTS audit_events (
 id bigserial PRIMARY KEY, action text NOT NULL, resource_id text NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS login_limits (
 key text PRIMARY KEY, attempts integer NOT NULL, resets_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS service_settings(key text PRIMARY KEY, value text NOT NULL);
