package store

import (
	"context"
	"fmt"
	"time"
)

type Read struct {
	Type, ID string
	Price    int64
}

// RecordAttempt commits resource accounting and reservation settlement together.
// On an uncertain result the reservation is retained at its maximum amount.
func (s *Store) RecordAttempt(ctx context.Context, reservation int64, runID, endpoint string, at time.Time, status int, reads []Read, certain bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var reservedRun, state string
	if err = tx.QueryRowContext(ctx, `SELECT run_id,state FROM budget_reservations WHERE id=?`, reservation).Scan(&reservedRun, &state); err != nil {
		return err
	}
	if reservedRun != runID || state != "reserved" {
		return fmt.Errorf("reservation does not belong to this unsettled attempt")
	}
	var cost int64
	if certain {
		for _, r := range reads {
			if r.ID == "" || r.Type == "" || r.Price <= 0 || r.Price > 1000000000 {
				return fmt.Errorf("invalid resource accounting")
			}
			res, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO billable_reads(day,resource_type,resource_id,price_microusd,run_id) VALUES(?,?,?,?,?)`, at.UTC().Format("2006-01-02"), r.Type, r.ID, r.Price, runID)
			if e != nil {
				return e
			}
			n, e := res.RowsAffected()
			if e != nil {
				return e
			}
			if n == 1 {
				cost += r.Price
			}
		}
		res, e := tx.ExecContext(ctx, `UPDATE budget_reservations SET amount_microusd=?,state='settled' WHERE id=? AND state='reserved'`, cost, reservation)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("actual estimate exceeds reservation or reservation invalid")
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO api_requests(run_id,endpoint,requested_at,status,resources,estimated_microusd) VALUES(?,?,?,?,?,?)`, runID, endpoint, at.UTC().Format("2006-01-02T15:04:05.000000000Z"), status, len(reads), cost)
	if err != nil {
		return err
	}
	return tx.Commit()
}
