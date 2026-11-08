package syncer

import (
	"context"
	"encoding/json"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
)

func TestMetadataFetchesOnlyNewPosts(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.SavePage(ctx, "bookmark", []store.Post{{ID: "1", AuthorID: "10"}}, store.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SavePage(ctx, "like", []store.Post{{ID: "1", AuthorID: "10"}}, store.Now()); e != nil {
		t.Fatal(e)
	}
	c := xapi.New("test")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	calls := 0
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("ids") != "1" || r.URL.Query().Get("expansions") == "" {
			t.Error("unexpected lookup")
		}
		return mockResponse(200, `{"data":[{"id":"1","author_id":"10"}],"includes":{"users":[{"id":"10","username":"example"}]}}`), nil
	})}
	if e = ResolveMetadata(ctx, s, c); e != nil {
		t.Fatal(e)
	}
	if e = ResolveMetadata(ctx, s, c); e != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, e)
	}
	ids, e := s.PendingMetadata(ctx, 10)
	if e != nil || len(ids) != 0 {
		t.Fatalf("pending %v %v", ids, e)
	}
}

func TestCachedAuthorAvoidsMetadataLookup(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.SaveIncludes(ctx, []json.RawMessage{json.RawMessage(`{"id":"10","username":"example"}`)}, nil, store.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SavePage(ctx, "like", []store.Post{{ID: "2", AuthorID: "10"}}, store.Now()); e != nil {
		t.Fatal(e)
	}
	ids, e := s.PendingMetadata(ctx, 10)
	if e != nil || len(ids) != 0 {
		t.Fatalf("cached author not reused: %v %v", ids, e)
	}
}
