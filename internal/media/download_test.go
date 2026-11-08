package media

import (
	"context"
	"encoding/json"
	"github.com/hayatky/xstash-cli/internal/store"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowedHost(t *testing.T) {
	for _, tt := range []struct {
		url string
		ok  bool
	}{{"https://pbs.twimg.com/media/x", true}, {"https://video.twimg.com/a", true}, {"http://pbs.twimg.com/a", false}, {"https://pbs.twimg.com.evil.invalid/a", false}, {"https://user@pbs.twimg.com/a", false}, {"https://127.0.0.1/a", false}} {
		if allowed(tt.url) != tt.ok {
			t.Fatalf("%s", tt.url)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDownloadWritesFileAndSkipsExisting(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.SaveIncludes(ctx, nil, []json.RawMessage{json.RawMessage(`{"media_key":"m1","type":"photo","url":"https://pbs.twimg.com/media/a"}`)}, store.Now()); e != nil {
		t.Fatal(e)
	}
	calls := 0
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("image"))}, nil
	})}
	n, e := download(ctx, s, filepath.Join(dir, "media"), client)
	if e != nil || n != 1 {
		t.Fatalf("count=%d err=%v", n, e)
	}
	var path string
	if e = s.DB.QueryRow(`SELECT local_path FROM media WHERE media_key='m1'`).Scan(&path); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "image" {
		t.Fatalf("read: %v", e)
	}
	if _, e = download(ctx, s, filepath.Join(dir, "media"), client); e != nil || calls != 1 {
		t.Fatalf("duplicate calls=%d error=%v", calls, e)
	}
}

func TestRetryMediaRateLimit(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		h := make(http.Header)
		h.Set("Retry-After", "0")
		if calls == 1 {
			return &http.Response{StatusCode: 429, Header: h, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	req, e := http.NewRequest("GET", "https://pbs.twimg.com/a", nil)
	if e != nil {
		t.Fatal(e)
	}
	resp, e := getWithRetry(context.Background(), client, req)
	if e != nil || resp.StatusCode != 200 || calls != 2 {
		t.Fatalf("calls=%d error=%v", calls, e)
	}
	resp.Body.Close()
}
