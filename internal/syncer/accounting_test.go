package syncer

import (
	"context"
	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/store"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestEstimatesDoNotBlockRequests(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c := config.Default()
	c.MediaMicrousd = 5000
	a := Accountant{Store: s, Config: c, RunID: "r"}
	ctx := context.Background()
	q := url.Values{"max_results": {"5"}, "expansions": {"author_id,attachments.media_keys"}}
	if e = a.Before(ctx, "/2/users/123/bookmarks", q); e != nil {
		t.Fatal(e)
	}
	if e = a.After(ctx, "/2/users/123/bookmarks", 200, []byte(`{"data":[{"id":"1"},{"id":"2"}],"includes":{"users":[{"id":"10"}]},"meta":{"result_count":2}}`), nil); e != nil {
		t.Fatal(e)
	}
	var count int
	var price int64
	if e = s.DB.QueryRow(`SELECT count(*),sum(price_microusd) FROM billable_reads`).Scan(&count, &price); e != nil || count != 3 || price != 20000 {
		t.Fatalf("count=%d price=%d err=%v", count, price, e)
	}
	if e = a.Before(ctx, "/2/users/123/bookmarks", q); e != nil {
		t.Fatalf("second request blocked: %v", e)
	}
	limit, e := a.LookupLimit(ctx)
	if e != nil || limit != 5 {
		t.Fatalf("lookup limit=%d err=%v", limit, e)
	}
}

func TestMalformedResponseRetainsReservation(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	c := config.Default()
	c.MediaMicrousd = 5000
	a := Accountant{Store: s, Config: c, RunID: "r"}
	q := url.Values{"max_results": {"5"}}
	if e = a.Before(ctx, "/2/users/123/bookmarks", q); e != nil {
		t.Fatal(e)
	}
	if e = a.After(ctx, "/2/users/123/bookmarks", 200, []byte(`{}`), nil); e != nil {
		t.Fatal(e)
	}
	var state string
	var cost int64
	if e = s.DB.QueryRow(`SELECT state,amount_microusd FROM budget_reservations`).Scan(&state, &cost); e != nil || state != "reserved" || cost != 25000 {
		t.Fatalf("state=%s cost=%d err=%v", state, cost, e)
	}
}

func TestRequestCrossingBudgetMonthKeepsBothPeriods(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	conf := config.Default()
	conf.MediaMicrousd = 5000
	clock := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)
	a := Accountant{Store: s, Config: conf, RunID: "r", Clock: func() time.Time { return clock }}
	if e = a.Before(ctx, "/2/users/123/bookmarks", url.Values{"max_results": {"5"}}); e != nil {
		t.Fatal(e)
	}
	clock = clock.Add(2 * time.Second)
	if e = a.After(ctx, "/2/users/123/bookmarks", 200, []byte(`{"data":[{"id":"1"}],"meta":{"result_count":1}}`), nil); e != nil {
		t.Fatal(e)
	}
	var old, new int64
	if e = s.DB.QueryRow(`SELECT coalesce(sum(amount_microusd),0) FROM budget_reservations WHERE substr(created_at,1,7)='2026-01'`).Scan(&old); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(`SELECT coalesce(sum(amount_microusd),0) FROM budget_reservations WHERE substr(created_at,1,7)='2026-02'`).Scan(&new); e != nil {
		t.Fatal(e)
	}
	if old != 25000 || new != 5000 {
		t.Fatalf("old=%d new=%d", old, new)
	}
}

func TestLegacyCapDoesNotStopAndLookupBatchesStaySmall(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	c := config.Default()
	c.MediaMicrousd = 5000
	a := Accountant{Store: s, Config: c, RunID: "r"}
	for i := 0; i < 20; i++ {
		if e = a.Before(ctx, "/2/users/123/bookmarks", url.Values{"max_results": {"100"}}); e != nil {
			t.Fatalf("request %d stopped: %v", i, e)
		}
	}
	var total int64
	if e = s.DB.QueryRow(`SELECT sum(amount_microusd) FROM budget_reservations`).Scan(&total); e != nil || total <= 2000000 {
		t.Fatalf("total=%d error=%v", total, e)
	}
	n, e := a.LookupLimit(ctx)
	if e != nil || n != 5 {
		t.Fatalf("limit=%d err=%v", n, e)
	}
}
