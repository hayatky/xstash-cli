package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"net/url"
	"strings"
)

func ResolveMetadata(ctx context.Context, s *store.Store, c *xapi.Client, maxPosts int) error {
	if maxPosts < 0 {
		return fmt.Errorf("metadata lookup limit must be nonnegative")
	}
	var incomplete []error
	lookedUp := 0
	attempted := map[string]bool{}
	for {
		if err := s.PruneCachedMetadata(ctx); err != nil {
			return err
		}
		pending, err := s.PendingMetadata(ctx, 1000000)
		if err != nil {
			return err
		}
		ids := []string{}
		for _, id := range pending {
			if !attempted[id] && len(ids) < 100 {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return errors.Join(incomplete...)
		}
		if maxPosts > 0 {
			remaining := maxPosts - lookedUp
			if remaining == 0 {
				return errors.Join(incomplete...)
			}
			if len(ids) > remaining {
				ids = ids[:remaining]
			}
		}
		if c.LookupLimit != nil {
			limit, e := c.LookupLimit(ctx)
			if e != nil {
				if err = s.MetadataReason(ctx, ids, e.Error()); err != nil {
					return err
				}
				return e
			}
			if limit < 1 || limit > 100 {
				return fmt.Errorf("lookup batch size must be 1..100")
			}
			if limit < len(ids) {
				ids = ids[:limit]
			}
		}
		lookedUp += len(ids)
		for _, id := range ids {
			attempted[id] = true
		}
		q := url.Values{"ids": {strings.Join(ids, ",")}, "tweet.fields": {"author_id,created_at,referenced_tweets,attachments"}, "expansions": {"author_id,attachments.media_keys"}, "user.fields": {"name,username"}, "media.fields": {"url,preview_image_url,variants,type"}}
		body, _, err := c.Get(ctx, "/2/tweets", q)
		if err != nil {
			if e := s.MetadataReason(ctx, ids, err.Error()); e != nil {
				return e
			}
			return err
		}
		var page xapi.Page
		if err = json.Unmarshal(body, &page); err != nil {
			return err
		}
		posts, err := xapi.ParsePosts(page.Data)
		if err != nil {
			return err
		}
		if err = s.SaveMetadata(ctx, posts, page.Includes.Users, page.Includes.Media, store.Now()); err != nil {
			return err
		}
		found := map[string]bool{}
		for _, p := range posts {
			found[p.ID] = true
		}
		var missing []string
		for _, id := range ids {
			if !found[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			if err = s.MetadataReason(ctx, missing, "lookup did not return post"); err != nil {
				return err
			}
			incomplete = append(incomplete, fmt.Errorf("metadata lookup did not return %d posts", len(missing)))
		}
	}
}
