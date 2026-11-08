// Package xapi implements read-only access to X API endpoints.
package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hayatky/xstash-cli/internal/store"
)

type Client struct {
	BaseURL     string
	Token       string
	HTTP        *http.Client
	MaxAttempts int
	Sleep       func(context.Context, time.Duration) error
	// BeforeRequest records a conservative estimate for each attempt.
	BeforeRequest  func(context.Context, string, url.Values) error
	AfterRequest   func(context.Context, string, int, []byte, error) error
	OnUnauthorized func(context.Context) (string, error)
	LookupLimit    func(context.Context) (int, error)
}
type Page struct {
	Data []json.RawMessage `json:"data"`
	Meta struct {
		NextToken   string `json:"next_token"`
		ResultCount *int   `json:"result_count"`
	} `json:"meta"`
	Errors   []json.RawMessage `json:"errors"`
	Includes struct {
		Users []json.RawMessage `json:"users"`
		Media []json.RawMessage `json:"media"`
	} `json:"includes"`
}

func New(token string) *Client {
	return &Client{BaseURL: "https://api.x.com", Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}, MaxAttempts: 3, Sleep: sleep}
}
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (c *Client) Get(ctx context.Context, path string, q url.Values) ([]byte, int, error) {
	if c.HTTP == nil || c.Sleep == nil || c.MaxAttempts < 1 || c.BeforeRequest == nil {
		return nil, 0, fmt.Errorf("invalid API client configuration")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, 0, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	for attempt := 0; attempt < c.MaxAttempts; attempt++ {
		if err := c.BeforeRequest(ctx, path, q); err != nil {
			return nil, 0, fmt.Errorf("GET %s: cost accounting: %w", path, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		client := *c.HTTP
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := client.Do(req)
		if err != nil {
			if c.AfterRequest != nil {
				if e := c.AfterRequest(ctx, path, 0, nil, err); e != nil {
					return nil, 0, e
				}
			}
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			if attempt+1 == c.MaxAttempts {
				return nil, 0, fmt.Errorf("GET %s: request failed", path)
			}
			if err = c.Sleep(ctx, time.Duration(1<<attempt)*time.Second); err != nil {
				return nil, 0, err
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if c.AfterRequest != nil {
			if e := c.AfterRequest(ctx, path, resp.StatusCode, body, readErr); e != nil {
				return nil, resp.StatusCode, e
			}
		}
		if readErr != nil {
			return nil, resp.StatusCode, fmt.Errorf("GET %s: response read failed", path)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, resp.StatusCode, nil
		}
		if resp.StatusCode == http.StatusUnauthorized && c.OnUnauthorized != nil && attempt+1 < c.MaxAttempts {
			token, e := c.OnUnauthorized(ctx)
			if e != nil {
				return nil, resp.StatusCode, e
			}
			c.Token = token
			continue
		}
		if (resp.StatusCode == 429 || resp.StatusCode >= 500) && attempt+1 < c.MaxAttempts {
			delay := time.Duration(1<<attempt) * time.Second
			if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n >= 0 {
				delay = time.Duration(n) * time.Second
			}
			if reset, e := strconv.ParseInt(resp.Header.Get("x-rate-limit-reset"), 10, 64); e == nil {
				if d := time.Until(time.Unix(reset, 0)); d > delay {
					delay = d
				}
			}
			if delay > 60*time.Second {
				return nil, resp.StatusCode, fmt.Errorf("GET %s: HTTP %d, retry after %s", path, resp.StatusCode, delay.Round(time.Second))
			}
			if err = c.Sleep(ctx, delay); err != nil {
				return nil, resp.StatusCode, err
			}
			continue
		}
		return nil, resp.StatusCode, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return nil, 0, fmt.Errorf("GET %s: retries exhausted", path)
}
func ParsePosts(raw []json.RawMessage) ([]store.Post, error) {
	posts := make([]store.Post, 0, len(raw))
	seen := map[string]bool{}
	for _, b := range raw {
		var p store.Post
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		if p.ID == "" {
			return nil, fmt.Errorf("API post missing id")
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("API response contains duplicate post ID %s", p.ID)
		}
		seen[p.ID] = true
		p.Raw = append([]byte(nil), b...)
		posts = append(posts, p)
	}
	return posts, nil
}
func (c *Client) Page(ctx context.Context, userID, kind string, size int, token string) (Page, int, error) {
	var p Page
	var endpoint string
	switch kind {
	case "bookmark":
		endpoint = "bookmarks"
		if size < 1 || size > 100 {
			return p, 0, fmt.Errorf("bookmark page size must be 1..100")
		}
	case "like":
		endpoint = "liked_tweets"
		if size < 5 || size > 100 {
			return p, 0, fmt.Errorf("like page size must be 5..100")
		}
	default:
		return p, 0, fmt.Errorf("invalid relation kind")
	}
	if userID == "" || strings.Trim(userID, "0123456789") != "" {
		return p, 0, fmt.Errorf("invalid user ID")
	}
	q := url.Values{"max_results": {strconv.Itoa(size)}, "tweet.fields": {"author_id,created_at,referenced_tweets,attachments"}}
	if token != "" {
		q.Set("pagination_token", token)
	}
	b, status, err := c.Get(ctx, "/2/users/"+userID+"/"+endpoint, q)
	if err != nil {
		return p, status, err
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return p, status, fmt.Errorf("decode %s response: %w", endpoint, err)
	}
	if p.Meta.ResultCount == nil || *p.Meta.ResultCount != len(p.Data) {
		return p, status, fmt.Errorf("invalid %s response: result_count does not match data", endpoint)
	}
	return p, status, nil
}
