package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRelationsAreIndependentAndIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	p := Post{ID: "1", Text: "hello", References: []Reference{{ID: "2", Type: "quoted"}}}
	for _, kind := range []string{"bookmark", "like", "bookmark"} {
		if _, err = s.SavePage(ctx, kind, []Post{p}, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	for table, want := range map[string]int{"posts": 1, "relations": 2, "quote_queue": 1, "refs": 1} {
		var got int
		if err = s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: got %d, err %v", table, got, err)
		}
	}
	if _, err = s.SavePage(ctx, "bookmark", []Post{{ID: "3"}, {}}, "2026-01-02T00:00:00Z"); err == nil {
		t.Fatal("expected failed transaction")
	}
	if ok, err := s.HasPost(ctx, "3"); err != nil || ok {
		t.Fatalf("partial transaction committed: %v %v", ok, err)
	}
}
func TestRejectsLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE posts(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("legacy database must be refused")
	}
}

func TestDatabaseRejectsDifferentAccount(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.BindOwner(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	if err = s.BindOwner(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	if err = s.BindOwner(ctx, "2"); err == nil {
		t.Fatal("different account accepted")
	}
}

func TestUpdatedPostReplacesReferencesAndMedia(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	p := Post{ID: "1", References: []Reference{{ID: "2", Type: "quoted"}}}
	p.Attachments.MediaKeys = []string{"old"}
	if _, err = s.SavePage(ctx, "like", []Post{p}, Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SavePage(ctx, "like", []Post{{ID: "1", Text: "updated"}}, Now()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT count(*) FROM refs WHERE source_id='1'`, `SELECT count(*) FROM post_media WHERE post_id='1'`} {
		var count int
		if err = s.DB.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("stale relation retained: %d %v", count, err)
		}
	}
}

func TestExistingPostWithNewMediaIsQueued(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.SaveIncludes(ctx, []json.RawMessage{json.RawMessage(`{"id":"10"}`)}, nil, Now()); err != nil {
		t.Fatal(err)
	}
	p := Post{ID: "1", AuthorID: "10"}
	if _, err = s.SavePage(ctx, "like", []Post{p}, Now()); err != nil {
		t.Fatal(err)
	}
	p.Attachments.MediaKeys = []string{"new"}
	if _, err = s.SavePage(ctx, "like", []Post{p}, Now()); err != nil {
		t.Fatal(err)
	}
	ids, err := s.PendingMetadata(ctx, 10)
	if err != nil || len(ids) != 1 {
		t.Fatalf("missing new media was not queued: %v %v", ids, err)
	}
}

func TestInterruptedRunIsRecoveredOnNextStart(t *testing.T) {
	ctx := context.Background()
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.StartRun(ctx, "old", Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`INSERT INTO sync_resume(kind,full,reason,updated_at) VALUES('like',1,'pending','time')`); e != nil {
		t.Fatal(e)
	}
	if e = s.StartRun(ctx, "new", Now()); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = s.DB.QueryRow(`SELECT status FROM sync_runs WHERE id='old'`).Scan(&status); e != nil || status != "failed" {
		t.Fatalf("stale status %s %v", status, e)
	}
	var count int
	if e = s.DB.QueryRow(`SELECT count(*) FROM sync_resume`).Scan(&count); e != nil || count != 1 {
		t.Fatal("resume work lost")
	}
}
