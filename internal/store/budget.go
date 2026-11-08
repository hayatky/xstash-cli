package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Reserve conservatively accounts for a request before it is issued. A caller
// must retain an uncertain reservation if a response is lost.
func (s *Store) Reserve(ctx context.Context, runID string, at time.Time, amount int64) (int64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("request estimate must be positive")
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO budget_reservations(run_id,created_at,amount_microusd,state) VALUES(?,?,?,'reserved')`, runID, at.UTC().Format("2006-01-02T15:04:05.000000000Z"), amount)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Settle replaces a reservation with a known estimate. Unknown outcomes retain
// the maximum estimate instead of assuming no charge.
func (s *Store) Settle(ctx context.Context, id, actual int64) error {
	if actual < 0 {
		return fmt.Errorf("negative cost")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE budget_reservations SET amount_microusd=?,state='settled' WHERE id=? AND state='reserved' AND amount_microusd>=?`, actual, id, actual)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// CarryReservation keeps the original maximum and books the same maximum in
// the response's UTC day when billing could have happened on either day.
func (s *Store) CarryReservation(ctx context.Context, id int64, at time.Time) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var run, state string
	var amount int64
	if err = tx.QueryRowContext(ctx, `SELECT run_id,amount_microusd,state FROM budget_reservations WHERE id=?`, id).Scan(&run, &amount, &state); err != nil {
		return 0, err
	}
	if state != "reserved" {
		return 0, fmt.Errorf("cannot carry settled reservation")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO budget_reservations(run_id,created_at,amount_microusd,state) VALUES(?,?,?,'reserved')`, run, at.UTC().Format("2006-01-02T15:04:05.000000000Z"), amount)
	if err != nil {
		return 0, err
	}
	next, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return next, tx.Commit()
}
