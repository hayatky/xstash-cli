package store

import (
	"context"
	"testing"
	"time"
)

func TestCostDetailsPreserveHistoricalPricesAndUTCDedup(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Date(2026, 11, 8, 23, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		offset   int
		endpoint string
		price    int64
	}{
		{0, "/2/users/123/bookmarks", 1000},
		{0, "/2/users/123/liked_tweets", 1000},
		{0, "/2/tweets", 5000},  // Cross-price precedence remains explicitly unverified.
		{1, "/2/tweets", 5000},  // A new UTC day is another priced resource-day.
		{31, "/2/tweets", 5000}, // Excluded from this month's breakdown.
	} {
		day := at.AddDate(0, 0, tc.offset)
		id, err := s.Reserve(ctx, "synthetic", day, tc.price)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordAttempt(ctx, id, "synthetic", tc.endpoint, day, 200, []Read{{Type: "post", ID: "1", Price: tc.price}}, true); err != nil {
			t.Fatal(err)
		}
	}
	details, err := s.CostDetails(ctx, "2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if details.ResourceDays != 2 || len(details.ResourcePrices) != 2 || len(details.Endpoints) != 3 {
		t.Fatalf("%+v", details)
	}
	var total int64
	for _, r := range details.ResourcePrices {
		total += r.EstimatedMicrousd
		if r.ResourceDays != 1 {
			t.Fatal(r)
		}
	}
	if total != 6000 {
		t.Fatalf("total %d", total)
	}
	var observed int
	var endpointTotal int64
	for _, e := range details.Endpoints {
		observed += e.ObservedResources
		endpointTotal += e.EstimatedMicrousd
		if e.Endpoint == "/2/users/123/bookmarks" {
			t.Fatal("account ID not redacted")
		}
	}
	if observed != 4 || endpointTotal != total {
		t.Fatalf("observed=%d total=%d", observed, endpointTotal)
	}
	empty, err := s.CostDetails(ctx, "2025-01")
	if err != nil || empty.ResourcePrices == nil || empty.Endpoints == nil || empty.ResourceDays != 0 {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	if _, err := s.CostDetails(ctx, "not a month"); err == nil {
		t.Fatal("invalid month accepted")
	}
}
