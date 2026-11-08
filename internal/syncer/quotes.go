package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"net/url"
	"strings"
)

type QuoteOptions struct {
	Depth    int
	MaxPosts int // Missing posts looked up per run; 0 means unlimited.
	Metadata bool
}

func ResolveQuotes(ctx context.Context, s *store.Store, c *xapi.Client, o QuoteOptions) error {
	if o.Depth < 0 || o.Depth > 3 || o.MaxPosts < 0 {
		return fmt.Errorf("invalid quote options")
	}
	lookedUp := 0
	attempted := map[string]bool{}
	for {
		work, err := s.PendingQuotes(ctx, 1000000)
		if err != nil {
			return err
		}
		var batch []store.QuoteWork
		var depth int
		for _, q := range work {
			if attempted[q.ID] || q.Depth > o.Depth {
				continue
			}
			if depth == 0 {
				depth = q.Depth
			}
			if q.Depth != depth || len(batch) == 100 {
				break
			}
			batch = append(batch, q)
		}
		if len(batch) == 0 {
			return nil
		}
		var ids []string
		for _, q := range batch {
			attempted[q.ID] = true
			has, e := s.HasPost(ctx, q.ID)
			if e != nil {
				return e
			}
			if has {
				if e = s.ResolveQuote(ctx, q.ID, nil, store.Now()); e != nil {
					return e
				}
				continue
			}
			ids = append(ids, q.ID)
		}
		if len(ids) == 0 {
			continue
		}
		if o.MaxPosts > 0 {
			remaining := o.MaxPosts - lookedUp
			if remaining == 0 {
				return nil
			}
			if len(ids) > remaining {
				ids = ids[:remaining]
			}
		}
		if c.LookupLimit != nil {
			limit, e := c.LookupLimit(ctx)
			if e != nil {
				for _, id := range ids {
					if err = s.SetQuoteFailure(ctx, id, "temporary_failure", e.Error()); err != nil {
						return err
					}
				}
				return e
			}
			if limit < 1 || limit > 100 {
				return fmt.Errorf("lookup batch size must be 1..100")
			}
			if limit < len(ids) {
				for _, id := range ids[limit:] {
					delete(attempted, id)
				}
				ids = ids[:limit]
			}
		}
		values := url.Values{"ids": {strings.Join(ids, ",")}, "tweet.fields": {"author_id,created_at,referenced_tweets,attachments"}}
		if o.Metadata {
			values.Set("expansions", "author_id,attachments.media_keys")
			values.Set("user.fields", "name,username")
			values.Set("media.fields", "url,preview_image_url,variants,type")
		}
		lookedUp += len(ids)
		b, _, e := c.Get(ctx, "/2/tweets", values)
		if e != nil {
			status := "temporary_failure"
			for _, id := range ids {
				if err = s.SetQuoteFailure(ctx, id, status, e.Error()); err != nil {
					return err
				}
			}
			return fmt.Errorf("quotes unresolved: %w", e)
		}
		var page xapi.Page
		if err = json.Unmarshal(b, &page); err != nil {
			return err
		}
		posts, err := xapi.ParsePosts(page.Data)
		if err != nil {
			return err
		}
		if err = s.SaveIncludes(ctx, page.Includes.Users, page.Includes.Media, store.Now()); err != nil {
			return err
		}
		found := map[string]bool{}
		for _, p := range posts {
			found[p.ID] = true
			if err = s.ResolveQuote(ctx, p.ID, &p, store.Now()); err != nil {
				return err
			}
		}
		for _, id := range ids {
			if found[id] {
				continue
			}
			status := "temporary_failure"
			reason := "lookup did not return a post"
			for _, raw := range page.Errors {
				var v struct {
					ResourceID string `json:"resource_id"`
					Title      string `json:"title"`
					Type       string `json:"type"`
				}
				_ = json.Unmarshal(raw, &v)
				if v.ResourceID == id {
					reason = v.Title
					low := strings.ToLower(v.Type + " " + v.Title)
					if strings.Contains(low, "deleted") {
						status = "deleted"
					} else if strings.Contains(low, "not found") || strings.Contains(low, "not-found") {
						status = "unavailable"
					} else if strings.Contains(low, "unauthorized") || strings.Contains(low, "forbidden") {
						status = "inaccessible"
					}
				}
			}
			if err = s.SetQuoteFailure(ctx, id, status, reason); err != nil {
				return err
			}
		}
	}
}
