package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Browser trust contains only a digest of the browser's random credential.
// The service owner is the sole writer through these methods.
func (store *Store) GrantBrowserTrust(ctx context.Context, digest []byte, at time.Time) error {
	if len(digest) != 32 {
		return errors.New("invalid browser trust digest")
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO browser_trust (credential_digest, granted_at) VALUES (?, ?)`, digest, at.UTC().Format(time.RFC3339Nano))
	return err
}

func (store *Store) BrowserTrusted(ctx context.Context, digest []byte) (bool, error) {
	if len(digest) != 32 {
		return false, nil
	}
	var present int
	err := store.db.QueryRowContext(ctx, `SELECT 1 FROM browser_trust WHERE credential_digest = ?`, digest).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (store *Store) ForgetBrowser(ctx context.Context, digest []byte) error {
	if len(digest) != 32 {
		return errors.New("invalid browser trust digest")
	}
	_, err := store.db.ExecContext(ctx, `DELETE FROM browser_trust WHERE credential_digest = ?`, digest)
	return err
}

func (store *Store) RevokeAllBrowsers(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM browser_trust`)
	return err
}
