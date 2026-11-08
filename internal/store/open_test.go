package store

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenExistingDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if s, err := OpenExisting(path); err == nil {
		s.Close()
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("database was created: %v", err)
	}
	for _, path := range []string{":memory:", t.TempDir()} {
		if s, err := OpenExisting(path); err == nil {
			s.Close()
			t.Fatalf("non-file %q accepted", path)
		}
	}
}

func TestOpenExistingPreservesV1Data(t *testing.T) {
	// URI punctuation must be treated as part of the filename, not DSN options.
	name := "data #% mode=memory.db"
	if runtime.GOOS != "windows" {
		name = "data ?#% mode=memory.db"
	}
	path := filepath.Join(t.TempDir(), name)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BindOwner(context.Background(), "synthetic-owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SavePage(context.Background(), "like", []Post{{ID: "123", Text: "synthetic"}}, "time"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err = OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Known(context.Background(), "like", "123"); err != nil || !ok {
		t.Fatalf("lost data: %v %v", ok, err)
	}
	if err = s.BindOwner(context.Background(), "other-owner"); err == nil {
		t.Fatal("lost owner")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("opening existing database modified its contents")
	}
}

func TestRejectsInvalidExistingDatabaseWithoutRepair(t *testing.T) {
	cases := []struct {
		name, mutation string
		fresh          bool
	}{
		{"empty file", "", true},
		{"foreign database", "CREATE TABLE unrelated(id TEXT)", true},
		{"marker only", "CREATE TABLE xstash_go_meta(version INTEGER NOT NULL); INSERT INTO xstash_go_meta VALUES(1)", true},
		{"missing marker", "DROP TABLE xstash_go_meta", false},
		{"empty metadata", "DELETE FROM xstash_go_meta", false},
		{"duplicate metadata", "INSERT INTO xstash_go_meta VALUES(1)", false},
		{"mixed metadata", "INSERT INTO xstash_go_meta VALUES(2)", false},
		{"future schema", "UPDATE xstash_go_meta SET version=2", false},
		{"malformed version", "UPDATE xstash_go_meta SET version='invalid'", false},
		{"missing index", "DROP INDEX idx_requests_date", false},
		{"wrong index", "DROP INDEX idx_requests_date; CREATE INDEX idx_requests_date ON api_requests(status)", false},
		{"missing foreign key", "DROP TABLE processing; CREATE TABLE processing(post_id TEXT NOT NULL,purpose TEXT NOT NULL,completed_at TEXT NOT NULL,PRIMARY KEY(post_id,purpose))", false},
		{"missing table", "DROP TABLE processing", false},
		{"missing column", "ALTER TABLE posts DROP COLUMN raw_json", false},
		{"wrong column type", "DROP TABLE processing; CREATE TABLE processing(post_id INTEGER NOT NULL REFERENCES posts(id),purpose TEXT NOT NULL,completed_at TEXT NOT NULL,PRIMARY KEY(post_id,purpose))", false},
		{"view replacing table", "DROP TABLE processing; CREATE VIEW processing AS SELECT id AS post_id, '' AS purpose, '' AS completed_at FROM posts", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "synthetic.db")
			if tc.fresh {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				s, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mutation != "" {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(tc.mutation); err != nil {
					t.Fatal(err)
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, opener := range []func(string) (*Store, error){OpenExisting, Open} {
				if s, err := opener(path); err == nil {
					s.Close()
					t.Fatal("invalid database accepted")
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("invalid database was modified")
				}
			}
		})
	}
}

func TestRejectsNonSQLiteAndSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-sqlite")
	if err := os.WriteFile(path, []byte("synthetic non-SQLite content"), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenExisting(path); err == nil {
		s.Close()
		t.Fatal("non-SQLite file accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if s, err := OpenExisting(link); err == nil {
		s.Close()
		t.Fatal("symlink accepted")
	}
}

func TestOpenMemoryStillWorks(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.SavePage(context.Background(), "bookmark", []Post{{ID: "synthetic"}}, "time"); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitIntegrityCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckIntegrity(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`PRAGMA foreign_keys=OFF; INSERT INTO processing(post_id,purpose,completed_at) VALUES('missing','synthetic','time');`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Ordinary opens validate schema metadata, not every existing data row.
	s, err = OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckIntegrity(); err == nil {
		t.Fatal("foreign key violation accepted by integrity check")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("integrity check modified database")
	}
}
