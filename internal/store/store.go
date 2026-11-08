// Package store owns the Go implementation's separate SQLite database.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

type Post struct {
	ID          string      `json:"id"`
	Text        string      `json:"text"`
	AuthorID    string      `json:"author_id"`
	CreatedAt   string      `json:"created_at"`
	References  []Reference `json:"referenced_tweets"`
	Attachments struct {
		MediaKeys []string `json:"media_keys"`
	} `json:"attachments"`
	Raw json.RawMessage `json:"-"`
}
type Reference struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			if err = f.Close(); err != nil {
				return nil, err
			}
		} else if !os.IsExist(err) {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	var tables, marker int
	if err = db.QueryRow(`SELECT count(*), coalesce(sum(name='xstash_go_meta'),0) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables, &marker); err != nil {
		db.Close()
		return nil, fmt.Errorf("inspect database: %w", err)
	}
	if tables > 0 && marker == 0 {
		db.Close()
		return nil, fmt.Errorf("database is not an xstash Go database; choose a separate database path")
	}
	if marker != 0 {
		var version int
		if err = db.QueryRow(`SELECT version FROM xstash_go_meta`).Scan(&version); err != nil || version != 1 {
			db.Close()
			return nil, fmt.Errorf("unsupported or invalid xstash Go database schema")
		}
	}
	if path != ":memory:" {
		if err = os.Chmod(path, 0600); err != nil {
			db.Close()
			return nil, err
		}
	}
	_, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
 BEGIN;
 CREATE TABLE IF NOT EXISTS xstash_go_meta(version INTEGER NOT NULL);
 INSERT INTO xstash_go_meta(version) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM xstash_go_meta);
 CREATE TABLE IF NOT EXISTS database_owner(singleton INTEGER PRIMARY KEY CHECK(singleton=1), user_id TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS posts(id TEXT PRIMARY KEY, text TEXT NOT NULL, author_id TEXT NOT NULL, created_at TEXT NOT NULL, raw_json TEXT NOT NULL, fetched_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS relations(kind TEXT NOT NULL CHECK(kind IN ('bookmark','like')), post_id TEXT NOT NULL REFERENCES posts(id), discovered_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, present INTEGER NOT NULL DEFAULT 1, PRIMARY KEY(kind,post_id));
 CREATE TABLE IF NOT EXISTS refs(source_id TEXT NOT NULL REFERENCES posts(id), target_id TEXT NOT NULL, kind TEXT NOT NULL, PRIMARY KEY(source_id,target_id,kind));
 CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY, raw_json TEXT NOT NULL, fetched_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS media(media_key TEXT PRIMARY KEY, raw_json TEXT NOT NULL, fetched_at TEXT NOT NULL, local_path TEXT);
 CREATE TABLE IF NOT EXISTS post_media(post_id TEXT NOT NULL REFERENCES posts(id), media_key TEXT NOT NULL, PRIMARY KEY(post_id,media_key));
 CREATE TABLE IF NOT EXISTS sync_state(kind TEXT PRIMARY KEY, completed_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sync_resume(kind TEXT PRIMARY KEY, full INTEGER NOT NULL, reason TEXT NOT NULL, updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sync_runs(id TEXT PRIMARY KEY, started_at TEXT NOT NULL, finished_at TEXT, status TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS sync_results(run_id TEXT NOT NULL, kind TEXT NOT NULL, new_relations INTEGER NOT NULL, new_posts INTEGER NOT NULL, retrieved INTEGER NOT NULL, pages INTEGER NOT NULL, reason TEXT NOT NULL, PRIMARY KEY(run_id,kind));
 CREATE TABLE IF NOT EXISTS quote_queue(id TEXT PRIMARY KEY, depth INTEGER NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS metadata_queue(id TEXT PRIMARY KEY REFERENCES posts(id), reason TEXT NOT NULL DEFAULT 'pending');
 CREATE TABLE IF NOT EXISTS processing(post_id TEXT NOT NULL REFERENCES posts(id), purpose TEXT NOT NULL, completed_at TEXT NOT NULL, PRIMARY KEY(post_id,purpose));
 CREATE TABLE IF NOT EXISTS api_requests(id INTEGER PRIMARY KEY, run_id TEXT NOT NULL, endpoint TEXT NOT NULL, requested_at TEXT NOT NULL, status INTEGER NOT NULL, resources INTEGER NOT NULL, new_posts INTEGER NOT NULL DEFAULT 0, estimated_microusd INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS billable_reads(day TEXT NOT NULL, resource_type TEXT NOT NULL, resource_id TEXT NOT NULL, price_microusd INTEGER NOT NULL, run_id TEXT NOT NULL, PRIMARY KEY(day,resource_type,resource_id));
 CREATE TABLE IF NOT EXISTS budget_reservations(id INTEGER PRIMARY KEY, run_id TEXT NOT NULL, created_at TEXT NOT NULL, amount_microusd INTEGER NOT NULL, state TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS idx_relations_seen ON relations(kind,last_seen_at);
 CREATE INDEX IF NOT EXISTS idx_requests_date ON api_requests(requested_at);
 COMMIT;`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func Now() string             { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }
func (s *Store) Known(ctx context.Context, kind, id string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM relations WHERE kind=? AND post_id=?)`, kind, id).Scan(&n)
	return n != 0, err
}
func (s *Store) HasPost(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM posts WHERE id=?)`, id).Scan(&n)
	return n != 0, err
}
func savePost(ctx context.Context, tx *sql.Tx, p Post, now string) error {
	raw := p.Raw
	if len(raw) == 0 {
		raw, _ = json.Marshal(p)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO posts VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET text=excluded.text,author_id=excluded.author_id,created_at=excluded.created_at,raw_json=excluded.raw_json,fetched_at=excluded.fetched_at`, p.ID, p.Text, p.AuthorID, p.CreatedAt, string(raw), now)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM refs WHERE source_id=?`, p.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id=?`, p.ID); err != nil {
		return err
	}
	for _, ref := range p.References {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO refs VALUES(?,?,?)`, p.ID, ref.ID, ref.Type); err != nil {
			return err
		}
	}
	for _, key := range p.Attachments.MediaKeys {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO post_media(post_id,media_key) VALUES(?,?)`, p.ID, key); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) SavePage(ctx context.Context, kind string, posts []Post, now string) (int, error) {
	return s.SavePageWithIncludes(ctx, kind, posts, nil, nil, now)
}
func (s *Store) SavePageWithIncludes(ctx context.Context, kind string, posts []Post, users, media []json.RawMessage, now string) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err = saveIncludes(ctx, tx, users, media, now); err != nil {
		return 0, err
	}
	n := 0
	for _, p := range posts {
		if p.ID == "" {
			return 0, fmt.Errorf("post missing id")
		}
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM relations WHERE kind=? AND post_id=?)`, kind, p.ID).Scan(&exists); err != nil {
			return 0, err
		}
		if exists == 0 {
			n++
		}
		if err = savePost(ctx, tx, p, now); err != nil {
			return 0, err
		}
		{
			missing := p.AuthorID == ""
			if p.AuthorID != "" {
				var found int
				if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, p.AuthorID).Scan(&found); err != nil {
					return 0, err
				}
				missing = found == 0
			}
			for _, key := range p.Attachments.MediaKeys {
				var found int
				if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE media_key=?)`, key).Scan(&found); err != nil {
					return 0, err
				}
				if found == 0 {
					missing = true
				}
			}
			if missing {
				if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO metadata_queue(id) VALUES(?)`, p.ID); err != nil {
					return 0, err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO relations(kind,post_id,discovered_at,last_seen_at) VALUES(?,?,?,?) ON CONFLICT(kind,post_id) DO UPDATE SET last_seen_at=excluded.last_seen_at,present=1`, kind, p.ID, now, now); err != nil {
			return 0, err
		}
		for _, ref := range p.References {
			if ref.Type == "quoted" {
				_, err = tx.ExecContext(ctx, `INSERT INTO quote_queue(id,depth,status) VALUES(?,1,'pending') ON CONFLICT(id) DO UPDATE SET status=CASE WHEN excluded.depth<quote_queue.depth AND quote_queue.status='resolved' THEN 'pending' ELSE quote_queue.status END,depth=min(quote_queue.depth,excluded.depth)`, ref.ID)
				if err != nil {
					return 0, err
				}
			}
		}
	}
	return n, tx.Commit()
}

func saveIncludes(ctx context.Context, tx *sql.Tx, users, media []json.RawMessage, now string) error {
	for _, u := range users {
		var v struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(u, &v); err != nil || v.ID == "" {
			return fmt.Errorf("invalid user resource")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO users VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET raw_json=excluded.raw_json,fetched_at=excluded.fetched_at`, v.ID, string(u), now); err != nil {
			return err
		}
	}
	for _, m := range media {
		var v struct {
			Key string `json:"media_key"`
		}
		if err := json.Unmarshal(m, &v); err != nil || v.Key == "" {
			return fmt.Errorf("invalid media resource")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO media(media_key,raw_json,fetched_at) VALUES(?,?,?) ON CONFLICT(media_key) DO UPDATE SET raw_json=excluded.raw_json,fetched_at=excluded.fetched_at`, v.Key, string(m), now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveIncludes(ctx context.Context, users, media []json.RawMessage, now string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveIncludes(ctx, tx, users, media, now); err != nil {
		return err
	}
	return tx.Commit()
}
