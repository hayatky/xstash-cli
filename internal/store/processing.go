package store

import (
	"context"
	"fmt"
)

type PendingPost struct {
	PostID    string `json:"post_id"`
	Text      string `json:"text"`
	AuthorID  string `json:"author_id"`
	CreatedAt string `json:"created_at"`
}

// Pending returns posts without changing their processing state.
func (s *Store) Pending(ctx context.Context, purpose string, limit int) ([]PendingPost, error) {
	if purpose == "" || limit < 1 {
		return nil, fmt.Errorf("purpose and positive limit are required")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id,p.text,p.author_id,p.created_at FROM posts p WHERE EXISTS (SELECT 1 FROM relations r WHERE r.post_id=p.id) AND NOT EXISTS (SELECT 1 FROM processing a WHERE a.post_id=p.id AND a.purpose=?) ORDER BY p.id LIMIT ?`, purpose, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PendingPost, 0)
	for rows.Next() {
		var p PendingPost
		if err = rows.Scan(&p.PostID, &p.Text, &p.AuthorID, &p.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func (s *Store) Complete(ctx context.Context, id, purpose, at string) error {
	if purpose == "" || id == "" {
		return fmt.Errorf("post ID and purpose are required")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO processing(post_id,purpose,completed_at) VALUES(?,?,?) ON CONFLICT(post_id,purpose) DO UPDATE SET completed_at=excluded.completed_at`, id, purpose, at)
	return err
}
