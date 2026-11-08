package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"net/url"
)

type Options struct {
	Kinds     []string
	Full      bool
	PageSize  int
	Threshold int
	MaxNew    int
}
type Result struct {
	Kind      string `json:"kind"`
	New       int    `json:"new_relations"`
	NewPosts  int    `json:"new_posts"`
	Retrieved int    `json:"retrieved_posts"`
	Pages     int    `json:"pages"`
	Complete  bool   `json:"complete"`
	Reason    string `json:"reason"`
}

func UserID(ctx context.Context, c *xapi.Client) (string, error) {
	b, _, err := c.Get(ctx, "/2/users/me", url.Values{})
	if err != nil {
		return "", err
	}
	var v struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal(b, &v); err != nil || v.Data.ID == "" {
		return "", fmt.Errorf("invalid users/me response")
	}
	return v.Data.ID, nil
}
func RunKind(ctx context.Context, s *store.Store, c *xapi.Client, user, kind string, o Options) (Result, error) {
	r := Result{Kind: kind}
	start := store.Now()
	cutoff, err := s.LastComplete(ctx, kind)
	if err != nil {
		return r, err
	}
	initial := cutoff == ""
	full := o.Full || initial
	boundary := Boundary{Initial: full, Threshold: o.Threshold}
	token := ""
	seenTokens := map[string]bool{}
	seenPosts := map[string]bool{}
	for {
		p, _, e := c.Page(ctx, user, kind, o.PageSize, token)
		if e != nil {
			r.Reason = e.Error()
			return r, e
		}
		posts, e := xapi.ParsePosts(p.Data)
		if e != nil {
			return r, e
		}
		stop := false
		newIDs := map[string]bool{}
		for _, post := range posts {
			if seenPosts[post.ID] {
				continue
			}
			seenPosts[post.ID] = true
			has, e := s.HasPost(ctx, post.ID)
			if e != nil {
				return r, e
			}
			if !has {
				newIDs[post.ID] = true
			}
			known, e := s.KnownAt(ctx, kind, post.ID, cutoff)
			if e != nil {
				return r, e
			}
			stop = boundary.Observe(known)
		}
		n, e := s.SavePageWithIncludes(ctx, kind, posts, p.Includes.Users, p.Includes.Media, store.Now())
		if e != nil {
			return r, e
		}
		r.New += n
		r.NewPosts += len(newIDs)
		r.Retrieved += len(posts)
		r.Pages++
		if len(p.Errors) > 0 {
			return r, fmt.Errorf("%s response contains %d partial errors", kind, len(p.Errors))
		}
		if p.Meta.NextToken == "" {
			r.Reason = "end"
			if err = s.FinishKind(ctx, kind, start, true); err != nil {
				return r, err
			}
			r.Complete = true
			return r, nil
		}
		if stop {
			r.Reason = "known_boundary"
			if err = s.FinishKind(ctx, kind, start, false); err != nil {
				return r, err
			}
			r.Complete = true
			return r, nil
		}
		if o.MaxNew > 0 && r.New >= o.MaxNew {
			r.Reason = "max_new"
			return r, nil
		}
		if seenTokens[p.Meta.NextToken] {
			return r, fmt.Errorf("%s repeated pagination token", kind)
		}
		seenTokens[p.Meta.NextToken] = true
		token = p.Meta.NextToken
	}
}
