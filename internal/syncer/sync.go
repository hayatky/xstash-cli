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
	Kinds        []string
	Full         bool
	PageSize     int
	Threshold    int
	MaxNew       int
	InitialLimit int
	MaxPages     int
}
type Result struct {
	Kind            string `json:"kind"`
	Mode            string `json:"mode"`
	New             int    `json:"new_relations"`
	NewPosts        int    `json:"new_posts"`
	Retrieved       int    `json:"retrieved_posts"`
	Pages           int    `json:"pages"`
	Complete        bool   `json:"complete"`
	HistoryComplete bool   `json:"history_complete"`
	Reason          string `json:"reason"`
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
	boundedInitial := initial && !o.Full && o.InitialLimit > 0
	r.Mode = "incremental"
	if o.Full {
		r.Mode = "full"
	} else if boundedInitial {
		r.Mode = "initial_window"
	} else if initial {
		r.Mode = "initial_full"
	}
	boundary := Boundary{Initial: full, Threshold: o.Threshold}
	token := ""
	seenTokens := map[string]bool{}
	seenPosts := map[string]bool{}
	for {
		size := o.PageSize
		if boundedInitial && o.InitialLimit-r.Retrieved < size {
			size = o.InitialLimit - r.Retrieved
			// Likes require at least five results. Keep all already-paid results.
			if kind == "like" && size < 5 {
				size = 5
			}
		}
		p, _, e := c.Page(ctx, user, kind, size, token)
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
			if boundary.Observe(known) {
				stop = true
			}
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
			r.HistoryComplete = true
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
		if boundedInitial && r.Retrieved >= o.InitialLimit {
			// A recent window is an incremental baseline, not proof that
			// older relations disappeared. Never reconcile omissions here.
			r.Reason = "initial_limit"
			if err = s.FinishKind(ctx, kind, start, false); err != nil {
				return r, err
			}
			r.Complete = true
			return r, nil
		}
		if o.MaxPages > 0 && r.Pages >= o.MaxPages {
			r.Reason = "max_pages"
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
