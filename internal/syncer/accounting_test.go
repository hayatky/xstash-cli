package syncer

import (
	"context"
	"fmt"
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

func TestOwnedPriceRequiresExactAccountAndSupportedEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, user, path string
		eligible         bool
		want             int64
		invalid          bool
	}{
		{"own bookmarks", "123", "/2/users/123/bookmarks", true, 1000, false},
		{"own likes", "123", "/2/users/123/liked_tweets", true, 1000, false},
		{"unconfirmed ownership", "123", "/2/users/123/bookmarks", false, 5000, false},
		{"different account", "123", "/2/users/456/liked_tweets", true, 5000, false},
		{"unknown account", "", "/2/users/123/bookmarks", true, 5000, false},
		{"identity not documented as owned", "123", "/2/users/me", true, 10000, false},
		{"lookup not owned", "123", "/2/tweets", true, 5000, false},
		{"unsupported endpoint", "123", "/other/bookmarks", true, 0, true},
		{"invalid account", "123", "/2/users/name/bookmarks", true, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := costTestStore(t)
			c := config.Default()
			c.OwnedEligible, c.UserID = tc.eligible, tc.user
			a := Accountant{Store: s, Config: c, RunID: "synthetic"}
			err := a.Before(context.Background(), tc.path, url.Values{"max_results": {"1"}, "ids": {"1"}})
			if tc.invalid {
				if err == nil {
					t.Fatal("unsupported endpoint accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var amount int64
			if err := s.DB.QueryRow(`SELECT amount_microusd FROM budget_reservations`).Scan(&amount); err != nil || amount != tc.want {
				t.Fatalf("amount=%d want=%d err=%v", amount, tc.want, err)
			}
		})
	}
}

func TestConfirmedErrorsAreUnbilledButUnknownOutcomesRemainReserved(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		readErr   error
		uncertain bool
	}{
		{"bad request", 400, `{"title":"Bad Request"}`, nil, false},
		{"unauthorized", 401, "", nil, false},
		{"forbidden", 403, "", nil, false},
		{"rate limited", 429, "", nil, false},
		{"server error", 500, "invalid json", nil, false},
		{"unavailable", 503, "", nil, false},
		{"read failure", 503, "", fmt.Errorf("read failed"), true},
		{"transport failure", 0, "", fmt.Errorf("timeout"), true},
		{"unfollowed redirect", 302, "", nil, true},
		{"invalid success", 200, `{}`, nil, true},
		{"null errors not proof of empty success", 200, `{"errors":null}`, nil, true},
		{"empty errors not proof of empty success", 200, `{"errors":[]}`, nil, true},
		{"object errors invalid", 200, `{"errors":{}}`, nil, true},
		{"null problem invalid", 200, `{"errors":[null]}`, nil, true},
		{"scalar problem invalid", 200, `{"errors":[1]}`, nil, true},
		{"empty problem invalid", 200, `{"errors":[{}]}`, nil, true},
		{"error only lookup", 200, `{"errors":[{"resource_id":"1","title":"Not Found"}]}`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := costTestStore(t)
			clock := time.Date(2026, 11, 30, 23, 59, 59, 0, time.UTC)
			a := Accountant{Store: s, Config: config.Default(), RunID: "synthetic", Clock: func() time.Time { return clock }}
			ctx := context.Background()
			if err := a.Before(ctx, "/2/tweets", url.Values{"ids": {"1"}}); err != nil {
				t.Fatal(err)
			}
			if !tc.uncertain && tc.status >= 400 {
				clock = clock.Add(2 * time.Second)
			}
			if err := a.After(ctx, "/2/tweets", tc.status, []byte(tc.body), tc.readErr); err != nil {
				t.Fatal(err)
			}
			var state string
			var amount int64
			var count int
			if err := s.DB.QueryRow(`SELECT count(*),state,amount_microusd FROM budget_reservations`).Scan(&count, &state, &amount); err != nil {
				t.Fatal(err)
			}
			wantState, wantAmount := "settled", int64(0)
			if tc.uncertain {
				wantState, wantAmount = "reserved", 5000
			}
			if count != 1 || state != wantState || amount != wantAmount {
				t.Fatalf("count=%d state=%s amount=%d", count, state, amount)
			}
			var attempts int
			if err := s.DB.QueryRow(`SELECT count(*) FROM api_requests`).Scan(&attempts); err != nil || attempts != 1 {
				t.Fatalf("attempts=%d err=%v", attempts, err)
			}
		})
	}
}

func TestPartialSuccessCountsOnlyReturnedResources(t *testing.T) {
	s := costTestStore(t)
	a := Accountant{Store: s, Config: config.Default(), RunID: "synthetic"}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := a.Before(ctx, "/2/tweets", url.Values{"ids": {"1,2,3"}}); err != nil {
			t.Fatal(err)
		}
		if err := a.After(ctx, "/2/tweets", 200, []byte(`{"data":[{"id":"1"}],"errors":[{"resource_id":"2","title":"Not Found"},{"resource_id":"3","title":"Forbidden"}]}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	var resources int
	var estimate int64
	if err := s.DB.QueryRow(`SELECT sum(resources),sum(estimated_microusd) FROM api_requests`).Scan(&resources, &estimate); err != nil || resources != 2 || estimate != 5000 {
		t.Fatalf("resources=%d estimate=%d err=%v", resources, estimate, err)
	}
}
