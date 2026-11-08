package syncer

import (
	"context"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mockResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestIncrementalPageCostAndFailureBoundary(t *testing.T) {
	run := func(size int) (int, int64) {
		s, e := store.Open(filepath.Join(t.TempDir(), "db"))
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		ctx := context.Background()
		known := make([]store.Post, 20)
		for i := range known {
			known[i] = store.Post{ID: strconv.Itoa(i + 5), Text: "known"}
		}
		if _, e = s.SavePage(ctx, "bookmark", known, store.Now()); e != nil {
			t.Fatal(e)
		}
		if e = s.FinishKind(ctx, "bookmark", store.Now(), false); e != nil {
			t.Fatal(e)
		}
		c := xapi.New("test")
		c.MaxAttempts = 1
		c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			page, _ := strconv.Atoi(r.URL.Query().Get("pagination_token"))
			count, _ := strconv.Atoi(r.URL.Query().Get("max_results"))
			start := page * count
			end := start + count
			if end > 24 {
				end = 24
			}
			parts := []string{}
			for i := start; i < end; i++ {
				parts = append(parts, fmt.Sprintf(`{"id":"%d","text":"p"}`, i+1))
			}
			next := ""
			if end < 24 {
				next = fmt.Sprintf(`,"next_token":"%d"`, page+1)
			}
			return mockResponse(200, fmt.Sprintf(`{"data":[%s],"meta":{"result_count":%d%s}}`, strings.Join(parts, ","), len(parts), next)), nil
		})}
		conf := config.Default()
		conf.MediaMicrousd = 5000
		conf.OwnedEligible = true
		a := Accountant{Store: s, Config: conf, RunID: "r"}
		a.Attach(c)
		r, e := RunKind(ctx, s, c, "123", "bookmark", Options{PageSize: size, Threshold: 5})
		if e != nil {
			t.Fatal(e)
		}
		var cost int64
		if e = s.DB.QueryRow(`SELECT SUM(estimated_microusd) FROM api_requests`).Scan(&cost); e != nil {
			t.Fatal(e)
		}
		return r.Retrieved, cost
	}
	small, smallCost := run(5)
	large, largeCost := run(100)
	if small != 10 || large != 24 || smallCost >= largeCost {
		t.Fatalf("small=%d/$%d large=%d/$%d", small, smallCost, large, largeCost)
	}
}
func TestQuoteLookupResumesAndCaches(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	_, e = s.SavePage(ctx, "like", []store.Post{{ID: "1", References: []store.Reference{{ID: "2", Type: "quoted"}}}}, store.Now())
	if e != nil {
		t.Fatal(e)
	}
	c := xapi.New("test")
	c.MaxAttempts = 1
	n := 0
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		n++
		switch r.URL.Query().Get("ids") {
		case "2":
			return mockResponse(200, `{"data":[{"id":"2","text":"q","referenced_tweets":[{"id":"3","type":"quoted"}]}]}`), nil
		case "3":
			return mockResponse(200, `{"data":[{"id":"3","text":"q2","referenced_tweets":[{"id":"4","type":"quoted"}]}]}`), nil
		case "4":
			return mockResponse(200, `{"data":[{"id":"4","text":"q3","referenced_tweets":[{"id":"5","type":"quoted"}]}]}`), nil
		}
		return mockResponse(404, ""), nil
	})}
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	if e = ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 3}); e != nil {
		t.Fatal(e)
	}
	if n != 3 {
		t.Fatalf("calls %d", n)
	}
	if e = ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 3}); e != nil || n != 3 {
		t.Fatalf("cache not reused %d %v", n, e)
	}
}

var _ = time.Second

func TestFailedIncrementalDoesNotAdvanceBoundary(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.SavePage(ctx, "like", []store.Post{{ID: "10", Text: "old"}}, store.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishKind(ctx, "like", store.Now(), false); err != nil {
		t.Fatal(err)
	}
	old, err := s.LastComplete(ctx, "like")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	c := xapi.New("test")
	c.MaxAttempts = 1
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return mockResponse(200, `{"data":[{"id":"11","text":"new"}],"meta":{"result_count":1,"next_token":"later"}}`), nil
		}
		return mockResponse(503, ""), nil
	})}
	if _, err = RunKind(ctx, s, c, "123", "like", Options{PageSize: 5, Threshold: 1}); err == nil {
		t.Fatal("expected error")
	}
	after, err := s.LastComplete(ctx, "like")
	if err != nil || after != old {
		t.Fatalf("failure advanced cutoff: %q %q %v", old, after, err)
	}
	known, err := s.KnownAt(ctx, "like", "11", old)
	if err != nil || known {
		t.Fatal("partial data became boundary")
	}
}
func TestBothRelationsSharePost(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := xapi.New("test")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return mockResponse(200, `{"data":[{"id":"42","text":"shared"}],"meta":{"result_count":1}}`), nil
	})}
	for _, kind := range []string{"bookmark", "like"} {
		r, e := RunKind(ctx, s, c, "123", kind, Options{PageSize: 5, Threshold: 5})
		if e != nil || r.New != 1 {
			t.Fatalf("%s: %+v %v", kind, r, e)
		}
	}
	var posts, relations int
	if e := s.DB.QueryRow(`SELECT count(*) FROM posts`).Scan(&posts); e != nil {
		t.Fatal(e)
	}
	if e := s.DB.QueryRow(`SELECT count(*) FROM relations`).Scan(&relations); e != nil {
		t.Fatal(e)
	}
	if posts != 1 || relations != 2 {
		t.Fatalf("%d posts %d relations", posts, relations)
	}
}

func TestInitialNeverStopsOnKnownPageAndFullMarksMissing(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.SavePage(ctx, "bookmark", []store.Post{{ID: "1"}, {ID: "2"}}, "2025-01-01T00:00:00.000000000Z"); e != nil {
		t.Fatal(e)
	}
	calls := 0
	c := xapi.New("test")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return mockResponse(200, `{"data":[{"id":"1"}],"meta":{"result_count":1,"next_token":"next"}}`), nil
		}
		return mockResponse(200, `{"data":[{"id":"3"}],"meta":{"result_count":1}}`), nil
	})}
	res, e := RunKind(ctx, s, c, "123", "bookmark", Options{PageSize: 1, Threshold: 1})
	if e != nil || calls != 2 || !res.Complete {
		t.Fatalf("initial res=%+v calls=%d err=%v", res, calls, e)
	}
	var present int
	if e = s.DB.QueryRow(`SELECT present FROM relations WHERE kind='bookmark' AND post_id='2'`).Scan(&present); e != nil || present != 0 {
		t.Fatalf("missing relation not reconciled: %d %v", present, e)
	}
}
func TestUnavailableQuoteRemainsVisible(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.SavePage(ctx, "like", []store.Post{{ID: "1", References: []store.Reference{{ID: "2", Type: "quoted"}}}}, store.Now()); e != nil {
		t.Fatal(e)
	}
	c := xapi.New("test")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return mockResponse(200, `{"errors":[{"resource_id":"2","title":"Not Found","type":"resource-not-found"}]}`), nil
	})}
	if e = ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 3}); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = s.DB.QueryRow(`SELECT status FROM quote_queue WHERE id='2'`).Scan(&status); e != nil || status != "unavailable" {
		t.Fatalf("status=%s error=%v", status, e)
	}
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return mockResponse(200, `{"data":[{"id":"2","text":"available again"}]}`), nil
	})}
	if e = ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 3}); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(`SELECT status FROM quote_queue WHERE id='2'`).Scan(&status); e != nil || status != "resolved" {
		t.Fatalf("retry status=%s err=%v", status, e)
	}
}

func TestLookupFailureLeavesQuotesForResume(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.SavePage(ctx, "like", []store.Post{{ID: "1", References: []store.Reference{{ID: "2", Type: "quoted"}}}}, store.Now()); e != nil {
		t.Fatal(e)
	}
	c := xapi.New("test")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.LookupLimit = func(context.Context) (int, error) { return 0, fmt.Errorf("invalid lookup settings") }
	if e = ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 3}); e == nil {
		t.Fatal("expected lookup settings error")
	}
	var status string
	if e = s.DB.QueryRow(`SELECT status FROM quote_queue WHERE id='2'`).Scan(&status); e != nil || status != "temporary_failure" {
		t.Fatalf("status=%s error=%v", status, e)
	}
	c.LookupLimit = func(context.Context) (int, error) { return 1, nil }
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return mockResponse(200, `{"data":[{"id":"2","text":"quote"}]}`), nil
	})}
	if e = ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 3}); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(`SELECT status FROM quote_queue WHERE id='2'`).Scan(&status); e != nil || status != "resolved" {
		t.Fatalf("resume: %s %v", status, e)
	}
}

func TestFinishFailureDoesNotReportComplete(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.DB.Exec(`CREATE TRIGGER fail_finish BEFORE INSERT ON sync_state BEGIN SELECT RAISE(ABORT,'test failure'); END`); e != nil {
		t.Fatal(e)
	}
	c := xapi.New("test")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return mockResponse(200, `{"data":[{"id":"1"}],"meta":{"result_count":1}}`), nil
	})}
	r, e := RunKind(ctx, s, c, "123", "like", Options{PageSize: 5, Threshold: 1})
	if e == nil || r.Complete {
		t.Fatalf("failed completion reported success: %+v %v", r, e)
	}
}
func TestRepeatedKnownIDCannotFormBoundary(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.SavePage(ctx, "like", []store.Post{{ID: "1"}}, store.Now()); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishKind(ctx, "like", store.Now(), false); e != nil {
		t.Fatal(e)
	}
	calls := 0
	c := xapi.New("test")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls < 3 {
			return mockResponse(200, fmt.Sprintf(`{"data":[{"id":"1"}],"meta":{"result_count":1,"next_token":"p%d"}}`, calls)), nil
		}
		return mockResponse(200, `{"data":[{"id":"2"}],"meta":{"result_count":1}}`), nil
	})}
	r, e := RunKind(ctx, s, c, "123", "like", Options{PageSize: 5, Threshold: 2})
	if e != nil || calls != 3 || r.New != 1 {
		t.Fatalf("premature boundary: %+v calls=%d err=%v", r, calls, e)
	}
}

func TestKnownBoundaryStaysLatchedAndPreservesWholePage(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.SavePage(ctx, "bookmark", []store.Post{{ID: "1"}}, store.Now()); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishKind(ctx, "bookmark", store.Now(), false); e != nil {
		t.Fatal(e)
	}
	calls := 0
	c := xapi.New("test")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return mockResponse(200, `{"data":[{"id":"1"},{"id":"2"}],"meta":{"result_count":2,"next_token":"next"}}`), nil
		}
		return mockResponse(200, `{"data":[{"id":"3"}],"meta":{"result_count":1}}`), nil
	})}
	r, e := RunKind(ctx, s, c, "123", "bookmark", Options{PageSize: 5, Threshold: 1})
	if e != nil || calls != 1 || r.New != 1 || r.Retrieved != 2 || r.Reason != "known_boundary" {
		t.Fatalf("boundary not latched: %+v %d %v", r, calls, e)
	}
	if has, err := s.HasPost(ctx, "2"); err != nil || !has {
		t.Fatal("already-fetched post after boundary lost")
	}
}
