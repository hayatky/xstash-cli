package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/xapi"
)

func costTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "synthetic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Offset tokens remain valid when the last initial page is shortened.
func listClient(t *testing.T, total int, calls *int) *xapi.Client {
	t.Helper()
	c := xapi.New("synthetic")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		*calls++
		offset, _ := strconv.Atoi(r.URL.Query().Get("pagination_token"))
		count, _ := strconv.Atoi(r.URL.Query().Get("max_results"))
		if count < 1 || count > 100 {
			t.Fatalf("bad size: %d", count)
		}
		end := offset + count
		if end > total {
			end = total
		}
		var posts []string
		for i := offset; i < end; i++ {
			posts = append(posts, fmt.Sprintf(`{"id":"%d","text":"synthetic"}`, i+1))
		}
		next := ""
		if end < total {
			next = fmt.Sprintf(`,"next_token":"%d"`, end)
		}
		return mockResponse(200, fmt.Sprintf(`{"data":[%s],"meta":{"result_count":%d%s}}`, strings.Join(posts, ","), len(posts), next)), nil
	})}
	return c
}

func TestBoundedBaselineThenIncrementalDoesNotRescanHistory(t *testing.T) {
	for _, kind := range []string{"bookmark", "like"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s := costTestStore(t)
			// Failed imports must not inflate the window by MaxNew or mark older
			// records absent merely because this selected window excludes them.
			if _, err := s.SavePage(ctx, kind, []store.Post{{ID: "1"}, {ID: "2"}, {ID: "9999"}}, "2020-01-01T00:00:00.000000000Z"); err != nil {
				t.Fatal(err)
			}
			limit := 100
			if kind == "like" {
				limit = 50
			}
			calls := 0
			c := listClient(t, 1000, &calls)
			o := Options{PageSize: 5, Threshold: 1, InitialLimit: limit, MaxPages: 20}
			r, err := RunKind(ctx, s, c, "123", kind, o)
			if err != nil || !r.Complete || r.HistoryComplete || r.Reason != "initial_limit" || r.Retrieved != limit || calls != limit/5 {
				t.Fatalf("baseline %+v calls=%d error=%v", r, calls, err)
			}
			cutoff, err := s.LastComplete(ctx, kind)
			if err != nil || cutoff == "" {
				t.Fatalf("no baseline: %q %v", cutoff, err)
			}
			var present int
			if err = s.DB.QueryRow(`SELECT present FROM relations WHERE kind=? AND post_id='9999'`, kind).Scan(&present); err != nil || present != 1 {
				t.Fatalf("bounded import marked old history absent: %d %v", present, err)
			}
			calls = 0
			r, err = RunKind(ctx, s, c, "123", kind, o)
			if err != nil || !r.Complete || r.Reason != "known_boundary" || calls != 1 || r.Retrieved != 5 {
				t.Fatalf("incremental %+v calls=%d error=%v", r, calls, err)
			}
		})
	}
}

func TestShortenedInitialPageAndLikeMinimumPreserveFetchedPosts(t *testing.T) {
	for _, tc := range []struct {
		kind              string
		limit, size, want int
	}{{"bookmark", 7, 5, 7}, {"like", 7, 5, 10}, {"bookmark", 7, 100, 7}, {"like", 50, 100, 50}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			s := costTestStore(t)
			calls := 0
			r, err := RunKind(context.Background(), s, listClient(t, 200, &calls), "123", tc.kind, Options{PageSize: tc.size, Threshold: 1, InitialLimit: tc.limit})
			if err != nil || !r.Complete || r.Retrieved != tc.want || r.New != tc.want {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

func TestRunCapNeverAdvancesExistingBoundary(t *testing.T) {
	for _, o := range []Options{{PageSize: 5, Threshold: 1, MaxPages: 1}, {PageSize: 5, Threshold: 1, MaxNew: 1}} {
		t.Run(fmt.Sprint(o), func(t *testing.T) {
			ctx := context.Background()
			s := costTestStore(t)
			if _, err := s.SavePage(ctx, "bookmark", []store.Post{{ID: "999"}}, store.Now()); err != nil {
				t.Fatal(err)
			}
			if err := s.FinishKind(ctx, "bookmark", store.Now(), false); err != nil {
				t.Fatal(err)
			}
			cutoff, _ := s.LastComplete(ctx, "bookmark")
			calls := 0
			r, err := RunKind(ctx, s, listClient(t, 200, &calls), "123", "bookmark", o)
			if err != nil || r.Complete || calls != 1 {
				t.Fatalf("cap %+v calls=%d error=%v", r, calls, err)
			}
			after, _ := s.LastComplete(ctx, "bookmark")
			if after != cutoff {
				t.Fatal("partial run advanced boundary")
			}
		})
	}
}

func TestExplicitFullBypassesInitialWindow(t *testing.T) {
	ctx := context.Background()
	s := costTestStore(t)
	calls := 0
	r, err := RunKind(ctx, s, listClient(t, 12, &calls), "123", "bookmark", Options{Full: true, PageSize: 5, Threshold: 1, InitialLimit: 5})
	if err != nil || !r.Complete || !r.HistoryComplete || r.Retrieved != 12 || calls != 3 {
		t.Fatalf("full %+v %d %v", r, calls, err)
	}
}

func TestQuoteDepthCapCacheAndDeferredWork(t *testing.T) {
	ctx := context.Background()
	s := costTestStore(t)
	posts := []store.Post{}
	for i := 1; i <= 4; i++ {
		posts = append(posts, store.Post{ID: strconv.Itoa(i), References: []store.Reference{{ID: strconv.Itoa(i + 10), Type: "quoted"}}})
	}
	if _, err := s.SavePage(ctx, "bookmark", posts, store.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePage(ctx, "like", []store.Post{{ID: "11"}}, store.Now()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	idsSeen := []string{}
	c := xapi.New("synthetic")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.LookupLimit = func(context.Context) (int, error) { return 1, nil }
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("expansions") != "" {
			t.Fatal("default quote requested costly metadata expansions")
		}
		id := r.URL.Query().Get("ids")
		idsSeen = append(idsSeen, id)
		return mockResponse(200, fmt.Sprintf(`{"data":[{"id":%q,"referenced_tweets":[{"id":"999","type":"quoted"}]}]}`, id)), nil
	})}
	if err := ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 1, MaxPosts: 2}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || strings.Join(idsSeen, ",") != "12,13" {
		t.Fatalf("lookups %v", idsSeen)
	}
	work, err := s.PendingQuotes(ctx, 100)
	if err != nil || len(work) != 2 {
		t.Fatalf("work=%+v error=%v", work, err)
	}
	if err = ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 1, MaxPosts: 2}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || idsSeen[2] != "14" {
		t.Fatalf("resume lookups %v", idsSeen)
	}
	if err = ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 0, MaxPosts: 2}); err != nil || calls != 3 {
		t.Fatalf("depth0 calls=%d %v", calls, err)
	}
	if err = ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 2, MaxPosts: 2}); err != nil || calls != 4 || idsSeen[3] != "999" {
		t.Fatalf("deeper resume %v %v", idsSeen, err)
	}
}

func TestMetadataCapAndCachedDetailsAvoidRepeatedPostLookup(t *testing.T) {
	ctx := context.Background()
	s := costTestStore(t)
	posts := []store.Post{{ID: "1", AuthorID: "a"}, {ID: "2", AuthorID: "b"}, {ID: "3", AuthorID: "c"}}
	if _, err := s.SavePage(ctx, "like", posts, store.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIncludes(ctx, []json.RawMessage{json.RawMessage(`{"id":"a","username":"cached"}`)}, nil, store.Now()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	c := xapi.New("synthetic")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		id := r.URL.Query().Get("ids")
		if id != "2" {
			t.Fatalf("unexpected lookup: %s", id)
		}
		return mockResponse(200, `{"data":[{"id":"2","author_id":"b"}],"includes":{"users":[{"id":"b"}]}}`), nil
	})}
	if err := ResolveMetadata(ctx, s, c, 1); err != nil || calls != 1 {
		t.Fatalf("metadata calls=%d %v", calls, err)
	}
	pending, err := s.PendingMetadata(ctx, 100)
	if err != nil || len(pending) != 1 || pending[0] != "3" {
		t.Fatalf("pending %v %v", pending, err)
	}
}

func TestAccountingRequiresOnlyRequestedResourcePrices(t *testing.T) {
	ctx := context.Background()
	s := costTestStore(t)
	conf := config.Default()
	a := Accountant{Store: s, Config: conf, RunID: "synthetic"}
	for _, p := range []struct {
		path string
		q    url.Values
	}{{"/2/users/123/bookmarks", url.Values{"max_results": {"5"}}}, {"/2/tweets", url.Values{"ids": {"1"}}}} {
		if err := a.Before(ctx, p.path, p.q); err != nil {
			t.Fatal(err)
		}
	}
	if limit, err := a.LookupLimit(ctx); err != nil || limit != 5 {
		t.Fatalf("limit %d %v", limit, err)
	}
	if err := a.Before(ctx, "/2/tweets", url.Values{"ids": {"1"}, "expansions": {"author_id,attachments.media_keys"}}); err == nil {
		t.Fatal("unknown media expansion price allowed")
	}
}

func TestQuoteCapRetriesFreshWorkBeforeUnavailablePrefix(t *testing.T) {
	ctx := context.Background()
	s := costTestStore(t)
	for i := 1; i <= 3; i++ {
		if _, err := s.SavePage(ctx, "bookmark", []store.Post{{ID: fmt.Sprintf("root%d", i), References: []store.Reference{{ID: strconv.Itoa(i), Type: "quoted"}}}}, store.Now()); err != nil {
			t.Fatal(err)
		}
	}
	c := xapi.New("synthetic")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.LookupLimit = func(context.Context) (int, error) { return 1, nil }
	seen := []string{}
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		id := r.URL.Query().Get("ids")
		seen = append(seen, id)
		if id == "3" {
			return mockResponse(200, `{"data":[{"id":"3"}]}`), nil
		}
		return mockResponse(200, fmt.Sprintf(`{"errors":[{"resource_id":%q,"title":"Not Found"}]}`, id)), nil
	})}
	for i := 0; i < 2; i++ {
		if err := ResolveQuotes(ctx, s, c, QuoteOptions{Depth: 1, MaxPosts: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 4 || seen[2] != "3" {
		t.Fatalf("pending work starved behind retries: %v", seen)
	}
}

func TestMetadataPrunesSharedDetailsAfterEveryBatch(t *testing.T) {
	for _, limit := range []int{1, 5} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			ctx := context.Background()
			s := costTestStore(t)
			if _, err := s.SavePage(ctx, "like", []store.Post{{ID: "1", AuthorID: "a"}, {ID: "2", AuthorID: "a"}}, store.Now()); err != nil {
				t.Fatal(err)
			}
			c := xapi.New("synthetic")
			c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
			c.LookupLimit = func(context.Context) (int, error) { return 1, nil }
			calls := 0
			c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls > 1 {
					t.Fatal("redundant lookup for shared cached author")
				}
				return mockResponse(200, `{"data":[{"id":"1","author_id":"a"}],"includes":{"users":[{"id":"a"}]}}`), nil
			})}
			if err := ResolveMetadata(ctx, s, c, limit); err != nil {
				t.Fatal(err)
			}
			pending, err := s.PendingMetadata(ctx, 100)
			if err != nil || len(pending) != 0 || calls != 1 {
				t.Fatalf("calls=%d pending=%v err=%v", calls, pending, err)
			}
		})
	}
}

func TestMetadataMissingBatchDoesNotStarveFreshWork(t *testing.T) {
	ctx := context.Background()
	s := costTestStore(t)
	if _, err := s.SavePage(ctx, "bookmark", []store.Post{{ID: "1", AuthorID: "a"}, {ID: "2", AuthorID: "b"}, {ID: "3", AuthorID: "c"}}, store.Now()); err != nil {
		t.Fatal(err)
	}
	c := xapi.New("synthetic")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.LookupLimit = func(context.Context) (int, error) { return 1, nil }
	seen := []string{}
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		id := r.URL.Query().Get("ids")
		seen = append(seen, id)
		if id == "3" {
			return mockResponse(200, `{"data":[{"id":"3","author_id":"c"}],"includes":{"users":[{"id":"c"}]}}`), nil
		}
		return mockResponse(200, `{"data":[]}`), nil
	})}
	if err := ResolveMetadata(ctx, s, c, 2); err == nil {
		t.Fatal("missing posts not reported")
	}
	if len(seen) != 2 {
		t.Fatalf("missing batch prevented unrelated lookup: %v", seen)
	}
	if err := ResolveMetadata(ctx, s, c, 2); err == nil {
		t.Fatal("missing retry not reported")
	}
	if len(seen) != 4 || seen[2] != "3" {
		t.Fatalf("pending metadata starved: %v", seen)
	}
}
