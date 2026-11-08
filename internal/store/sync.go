package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) LastComplete(ctx context.Context, kind string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT completed_at FROM sync_state WHERE kind=?`, kind).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
func (s *Store) KnownAt(ctx context.Context, kind, id, cutoff string) (bool, error) {
	if cutoff == "" {
		return false, nil
	}
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM relations WHERE kind=? AND post_id=? AND present=1 AND discovered_at<=?)`, kind, id, cutoff).Scan(&n)
	return n != 0, err
}
func (s *Store) StartRun(ctx context.Context, id, at string) error {
	// The caller holds the sync lock, so any earlier running record was interrupted.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE sync_runs SET status='failed',detail='interrupted before completion',finished_at=? WHERE status='running'`, at); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_runs(id,started_at,status) VALUES(?,?,'running')`, id, at); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) EndRun(ctx context.Context, id, status, detail, at string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sync_runs SET status=?,detail=?,finished_at=? WHERE id=?`, status, detail, at, id)
	return err
}
func (s *Store) FinishKind(ctx context.Context, kind, runStart string, full bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if full {
		if _, err = tx.ExecContext(ctx, `UPDATE relations SET present=0 WHERE kind=? AND last_seen_at<?`, kind, runStart); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_state(kind,completed_at) VALUES(?,?) ON CONFLICT(kind) DO UPDATE SET completed_at=excluded.completed_at`, kind, Now()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sync_resume WHERE kind=?`, kind); err != nil {
		return err
	}
	return tx.Commit()
}

// BindOwner prevents relations from different authenticated accounts sharing a database.
func (s *Store) BindOwner(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("empty account ID")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO database_owner(singleton,user_id) VALUES(1,?)`, id); err != nil {
		return err
	}
	var owner string
	if err = tx.QueryRowContext(ctx, `SELECT user_id FROM database_owner WHERE singleton=1`).Scan(&owner); err != nil {
		return err
	}
	if owner != id {
		return fmt.Errorf("database belongs to a different X account; use a separate XSTASH_HOME")
	}
	return tx.Commit()
}
