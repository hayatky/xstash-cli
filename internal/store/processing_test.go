package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestProcessingPerPurpose(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	p := []Post{{ID: "1", Text: "untrusted"}}
	for _, kind := range []string{"bookmark", "like"} {
		if _, err = s.SavePage(ctx, kind, p, "time"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		items, err := s.Pending(ctx, "analysis", 10)
		if err != nil || len(items) != 1 {
			t.Fatalf("read is not completion: %v %v", items, err)
		}
	}
	if err = s.Complete(ctx, "1", "analysis", "time"); err != nil {
		t.Fatal(err)
	}
	items, err := s.Pending(ctx, "analysis", 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("completed item remains: %v %v", items, err)
	}
	items, err = s.Pending(ctx, "reminder", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("purpose not independent: %v %v", items, err)
	}
}
