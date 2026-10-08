package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 20
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err = migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
func migrate(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(7483281)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, file := range files {
		content, err := migrations.ReadFile("migrations/" + file.Name())
		if err != nil {
			return err
		}
		h := sha256.Sum256(content)
		checksum := hex.EncodeToString(h[:])
		var old string
		if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT checksum FROM schema_migrations WHERE name=$1),'')`, file.Name()).Scan(&old); err != nil {
			return err
		}
		if old != "" {
			if old != checksum {
				return fmt.Errorf("migration %s checksum mismatch", file.Name())
			}
			continue
		}
		if _, err = tx.Exec(ctx, string(content)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, file.Name(), checksum); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// BindCredentialKey prevents accidental credential-key rotation from generating
// passwords that differ from grants already installed on gateways.
func BindCredentialKey(ctx context.Context, db *pgxpool.Pool, fingerprint string) error {
	_, e := db.Exec(ctx, `INSERT INTO service_settings(key,value) VALUES('credential_key_fingerprint',$1) ON CONFLICT DO NOTHING`, fingerprint)
	if e != nil {
		return e
	}
	var actual string
	if e = db.QueryRow(ctx, `SELECT value FROM service_settings WHERE key='credential_key_fingerprint'`).Scan(&actual); e != nil {
		return e
	}
	if actual != fingerprint {
		return fmt.Errorf("credential key differs from database binding; restore the original key or follow rotation procedure")
	}
	return nil
}
