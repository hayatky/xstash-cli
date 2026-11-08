package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAccountingDedupAndUncertainReservation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		id, err := s.Reserve(ctx, "run", now, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.RecordAttempt(ctx, id, "run", "/2/tweets", now, 200, []Read{{Type: "post", ID: "123", Price: 5000}}, true); err != nil {
			t.Fatal(err)
		}
	}
	id, err := s.Reserve(ctx, "run", now, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordAttempt(ctx, id, "run", "/2/tweets", now, 0, nil, false); err != nil {
		t.Fatal(err)
	}
	var total int64
	var attempts int
	if err = s.DB.QueryRow(`SELECT sum(amount_microusd) FROM budget_reservations`).Scan(&total); err != nil || total != 15000 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	if err = s.DB.QueryRow(`SELECT count(*) FROM api_requests`).Scan(&attempts); err != nil || attempts != 3 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
func TestEstimatesHaveNoMonthlyCap(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	now := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, e = s.Reserve(context.Background(), "r", now, 1500000); e != nil {
			t.Fatal(e)
		}
	}
	var total int64
	if e = s.DB.QueryRow(`SELECT sum(amount_microusd) FROM budget_reservations`).Scan(&total); e != nil || total != 4500000 {
		t.Fatalf("total=%d err=%v", total, e)
	}
}
