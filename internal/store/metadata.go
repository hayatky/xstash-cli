package store

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Store) PendingMetadata(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM metadata_queue ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Store) SaveMetadata(ctx context.Context, posts []Post, users, media []json.RawMessage, at string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveIncludes(ctx, tx, users, media, at); err != nil {
		return err
	}
	for _, p := range posts {
		if p.ID == "" {
			return fmt.Errorf("metadata post missing id")
		}
		if err = savePost(ctx, tx, p, at); err != nil {
			return err
		}
		var depth int
		if err = tx.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM relations WHERE post_id=?) THEN 0 ELSE coalesce((SELECT depth FROM quote_queue WHERE id=?),3) END`, p.ID, p.ID).Scan(&depth); err != nil {
			return err
		}
		for _, ref := range p.References {
			if ref.Type == "quoted" && depth < 3 {
				if _, err = tx.ExecContext(ctx, `INSERT INTO quote_queue(id,depth,status) VALUES(?,?,'pending') ON CONFLICT(id) DO UPDATE SET status=CASE WHEN excluded.depth<quote_queue.depth AND quote_queue.status='resolved' THEN 'pending' ELSE quote_queue.status END,depth=min(quote_queue.depth,excluded.depth)`, ref.ID, depth+1); err != nil {
					return err
				}
			}
		}
		missing := p.AuthorID == ""
		if p.AuthorID != "" {
			var found int
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, p.AuthorID).Scan(&found); err != nil {
				return err
			}
			missing = found == 0
		}
		for _, key := range p.Attachments.MediaKeys {
			var found int
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE media_key=?)`, key).Scan(&found); err != nil {
				return err
			}
			if found == 0 {
				missing = true
			}
		}
		if missing {
			_, err = tx.ExecContext(ctx, `UPDATE metadata_queue SET reason='author or media unavailable' WHERE id=?`, p.ID)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM metadata_queue WHERE id=?`, p.ID)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) MetadataReason(ctx context.Context, ids []string, reason string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE metadata_queue SET reason=? WHERE id=?`, reason, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
