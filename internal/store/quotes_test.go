package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestQuotesDepthAndCycle(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	ref := func(id string) []Reference { return []Reference{{ID: id, Type: "quoted"}} }
	if _, err = s.SavePage(ctx, "like", []Post{{ID: "root", References: ref("a")}}, "time"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Post{{ID: "a", References: ref("b")}, {ID: "b", References: ref("c")}, {ID: "c", References: ref("d")}} {
		q, err := s.PendingQuotes(ctx, 1)
		if err != nil || len(q) != 1 || q[0].ID != p.ID {
			t.Fatalf("queue=%v err=%v", q, err)
		}
		if err = s.ResolveQuote(ctx, p.ID, &p, "time"); err != nil {
			t.Fatal(err)
		}
	}
	q, err := s.PendingQuotes(ctx, 10)
	if err != nil || len(q) != 0 {
		t.Fatalf("depth exceeded: %v %v", q, err)
	}
}
func TestQuoteCacheAndCycle(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	p := Post{ID: "a", References: []Reference{{ID: "root", Type: "quoted"}}}
	if _, err = s.SavePage(ctx, "bookmark", []Post{{ID: "root", References: []Reference{{ID: "a", Type: "quoted"}}}, p}, "time"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		q, err := s.PendingQuotes(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(q) == 0 {
			return
		}
		for _, x := range q {
			if err = s.ResolveQuote(ctx, x.ID, nil, "time"); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Fatal("cycle did not terminate")
}

func TestQuoteMetadataPreservesDepth(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.SavePage(ctx, "like", []Post{{ID: "1", References: []Reference{{ID: "2", Type: "quoted"}}}}, Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO quote_queue(id,depth,status) VALUES('3',3,'pending')`); err != nil {
		t.Fatal(err)
	}
	p := Post{ID: "3", AuthorID: "10", References: []Reference{{ID: "4", Type: "quoted"}}}
	if err = s.ResolveQuote(ctx, "3", &p, Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMetadata(ctx, []Post{p}, []json.RawMessage{json.RawMessage(`{"id":"10"}`)}, nil, Now()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT count(*) FROM quote_queue WHERE id='4'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("metadata reset quote depth: %d %v", count, err)
	}
}
