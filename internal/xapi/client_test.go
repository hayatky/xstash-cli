package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestPageAndRateLimit(t *testing.T) {
	attempts := 0
	preflights, accounted := 0, 0
	c := New("test")
	c.BeforeRequest = func(context.Context, string, url.Values) error { preflights++; return nil }
	c.AfterRequest = func(context.Context, string, int, []byte, error) error { accounted++; return nil }
	c.HTTP = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if r.URL.Path != "/2/users/123/liked_tweets" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.URL.Query().Get("max_results") != "5" {
			t.Errorf("size %s", r.URL.RawQuery)
		}
		if attempts == 1 {
			return response(429, ""), nil
		}
		return response(200, `{"data":[{"id":"10","text":"hello"}],"meta":{"result_count":1,"next_token":"next"}}`), nil
	})}
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	p, _, err := c.Page(context.Background(), "123", "like", 5, "")
	if err != nil || len(p.Data) != 1 || p.Meta.NextToken != "next" || attempts != 2 || preflights != 2 || accounted != 2 {
		t.Fatalf("page=%+v attempts=%d err=%v", p, attempts, err)
	}
	posts, err := ParsePosts(p.Data)
	if err != nil || posts[0].ID != "10" || len(posts[0].Raw) == 0 {
		t.Fatalf("posts=%v err=%v", posts, err)
	}
}
func TestPageBounds(t *testing.T) {
	c := New("test")
	for _, tt := range []struct {
		kind string
		size int
	}{{"like", 4}, {"bookmark", 0}, {"bookmark", 101}} {
		if _, _, err := c.Page(context.Background(), "123", tt.kind, tt.size, ""); err == nil {
			t.Fatalf("accepted invalid size: %+v", tt)
		}
	}
}

func TestRejectDuplicatePostIDs(t *testing.T) {
	if _, err := ParsePosts([]json.RawMessage{json.RawMessage(`{"id":"1"}`), json.RawMessage(`{"id":"1"}`)}); err == nil {
		t.Fatal("accepted duplicate post IDs")
	}
}
func TestNoUnaccountedRedirect(t *testing.T) {
	calls := 0
	c := New("token")
	c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
	c.HTTP = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := response(302, "")
		resp.Header.Set("Location", "https://api.x.com/other")
		return resp, nil
	})}
	if _, _, err := c.Get(context.Background(), "/2/tweets", url.Values{}); err == nil || calls != 1 {
		t.Fatalf("redirect followed: calls=%d err=%v", calls, err)
	}
}

func TestRefreshFailureStopsLaterRequests(t *testing.T) {
	calls, refreshes, accounted := 0, 0, 0
	c := New("synthetic")
	c.HTTP = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { calls++; return response(401, "{}"), nil })}
	c.BeforeRequest = func(context.Context, string, url.Values) error { accounted++; return nil }
	c.OnUnauthorized = func(context.Context) (string, error) {
		refreshes++
		return "", fmt.Errorf("synthetic refresh persistence failure")
	}
	for i := 0; i < 3; i++ {
		if _, _, err := c.Get(context.Background(), "/2/tweets", url.Values{}); err == nil {
			t.Fatal("failed refresh accepted")
		}
	}
	if calls != 1 || refreshes != 1 || accounted != 1 {
		t.Fatalf("work continued after refresh failure: requests=%d refreshes=%d reservations=%d", calls, refreshes, accounted)
	}
}

func TestOversizedResponseRetainsUncertainOutcome(t *testing.T) {
	for _, status := range []int{200, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c := New("synthetic")
			c.BeforeRequest = func(context.Context, string, url.Values) error { return nil }
			calls := 0
			c.HTTP = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(status, strings.Repeat(" ", maxResponseBytes+1)), nil
			})}
			accounted := false
			c.AfterRequest = func(_ context.Context, _ string, gotStatus int, body []byte, readErr error) error {
				accounted = true
				if gotStatus != status || len(body) != maxResponseBytes || readErr == nil {
					t.Fatalf("status=%d len=%d readErr=%v", gotStatus, len(body), readErr)
				}
				return nil
			}
			if _, _, err := c.Get(context.Background(), "/2/tweets", url.Values{"ids": {"1"}}); err == nil || !accounted || calls != 1 {
				t.Fatalf("err=%v accounted=%v calls=%d", err, accounted, calls)
			}
		})
	}
}
