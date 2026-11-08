package store

import (
	"context"
	"database/sql"
	"fmt"
)

type QuoteWork struct {
	ID     string
	Depth  int
	Status string
	Reason string
}

func (s *Store) PendingQuotes(ctx context.Context, limit int) ([]QuoteWork, error) {
	if limit < 1 {
		return nil, fmt.Errorf("limit must be positive")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,depth,status,reason FROM quote_queue WHERE status IN ('pending','temporary_failure','budget','inaccessible','unavailable') ORDER BY depth,id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []QuoteWork
	for rows.Next() {
		var q QuoteWork
		if err = rows.Scan(&q.ID, &q.Depth, &q.Status, &q.Reason); err != nil {
			return nil, err
		}
		result = append(result, q)
	}
	return result, rows.Err()
}

// ResolveQuote stores a retrieved post and schedules its quoted children up to
// three edges from an original relation. Reprocessing a cache hit follows the
// same traversal and never requires an additional lookup.
func (s *Store) ResolveQuote(ctx context.Context, id string, post *Post, now string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var depth int
	if err = tx.QueryRowContext(ctx, `SELECT depth FROM quote_queue WHERE id=?`, id).Scan(&depth); err != nil {
		return err
	}
	if post != nil {
		if post.ID != id {
			return fmt.Errorf("quote ID mismatch")
		}
		if err = savePost(ctx, tx, *post, now); err != nil {
			return err
		}
		var authorFound int
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, post.AuthorID).Scan(&authorFound); err != nil {
			return err
		}
		missing := authorFound == 0
		for _, key := range post.Attachments.MediaKeys {
			var found int
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE media_key=?)`, key).Scan(&found); err != nil {
				return err
			}
			if found == 0 {
				missing = true
			}
		}
		if missing {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO metadata_queue(id) VALUES(?)`, post.ID); err != nil {
				return err
			}
		}
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM posts WHERE id=?)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `UPDATE quote_queue SET status='resolved',reason='' WHERE id=?`, id); err != nil {
		return err
	}
	if depth < 3 {
		rows, e := tx.QueryContext(ctx, `SELECT target_id FROM refs WHERE source_id=? AND kind='quoted'`, id)
		if e != nil {
			return e
		}
		var ids []string
		for rows.Next() {
			var child string
			if e = rows.Scan(&child); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, child)
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return e
		}
		rows.Close()
		for _, child := range ids {
			_, err = tx.ExecContext(ctx, `INSERT INTO quote_queue(id,depth,status) VALUES(?,?,'pending') ON CONFLICT(id) DO UPDATE SET status=CASE WHEN excluded.depth<quote_queue.depth AND quote_queue.status='resolved' THEN 'pending' ELSE quote_queue.status END,depth=min(quote_queue.depth,excluded.depth)`, child, depth+1)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (s *Store) SetQuoteFailure(ctx context.Context, id, status, reason string) error {
	switch status {
	case "budget", "temporary_failure", "deleted", "inaccessible", "unavailable":
	default:
		return fmt.Errorf("invalid quote status")
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE quote_queue SET status=?,reason=? WHERE id=?`, status, reason, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
