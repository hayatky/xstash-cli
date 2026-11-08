package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/auth"
	"github.com/hayatky/xstash-cli/internal/config"
	out "github.com/hayatky/xstash-cli/internal/export"
	"github.com/hayatky/xstash-cli/internal/lock"
	"github.com/hayatky/xstash-cli/internal/media"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/syncer"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"io"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "xstash:", err)
		os.Exit(1)
	}
}
func jsonOut(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func flags(name string, errout io.Writer) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(errout)
	return f
}
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: xstash <config|db|sync|export|stats|pending|complete>")
	}
	configPath, dbPath, err := config.Paths()
	if err != nil {
		return err
	}
	if args[0] == "sync" || args[0] == "config" && len(args) > 1 && args[1] != "show" {
		if err = os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
			return err
		}
		guard, e := lock.Acquire(configPath + ".lock")
		if e != nil {
			return e
		}
		defer guard.Close()
	}
	c, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err = config.ApplyEnv(&c, os.Getenv); err != nil {
		return err
	}
	cmd := args[0]
	args = args[1:]
	if cmd == "config" {
		if len(args) == 0 {
			return fmt.Errorf("usage: config <init|import|set|show>")
		}
		switch args[0] {
		case "show":
			return jsonOut(stdout, map[string]any{"client_id": c.ClientID, "user_id": c.UserID, "access_token_set": c.AccessToken != "", "refresh_token_set": c.RefreshToken != "", "expires_at": c.ExpiresAt, "token_type": c.TokenType, "scope": c.Scope, "scope_source": c.ScopeSource, "refresh_state": c.RefreshState, "refresh_started_at": c.RefreshStartedAt, "auth_metadata_valid": auth.Ready(c) == nil, "lookup_batch_size": c.LookupBatchSize, "owned_post_microusd": c.OwnedPostMicrousd, "post_microusd": c.PostMicrousd, "user_microusd": c.UserMicrousd, "media_microusd": c.MediaMicrousd, "owned_eligible": c.OwnedEligible})
		case "import":
			return importCommand(ctx, args[1:], stdout, stderr, &c, configPath)
		case "init":
			f := flags("config init", stderr)
			id := f.String("client-id", c.ClientID, "OAuth client ID")
			port := f.Int("port", 38080, "loopback port")
			if err = f.Parse(args[1:]); err != nil {
				return err
			}
			if c.ClientID != *id {
				c.ClientSecret = ""
				c.OwnedEligible = false
			}
			c.ClientID = *id
			return auth.Login(ctx, &c, configPath, *port, func(u string) { fmt.Fprintln(stderr, "Open this authorization URL:", u) })
		case "set":
			f := flags("config set", stderr)
			batch := f.Int("lookup-batch-size", c.LookupBatchSize, "quote and metadata lookup batch size 1..100")
			owned := f.Int64("owned-post-microusd", c.OwnedPostMicrousd, "owned read price")
			post := f.Int64("post-microusd", c.PostMicrousd, "post read price")
			user := f.Int64("user-microusd", c.UserMicrousd, "user read price")
			media := f.Int64("media-microusd", c.MediaMicrousd, "media read price; required")
			eligible := f.Bool("owned-eligible", c.OwnedEligible, "authenticated user owns the developer app")
			if err = f.Parse(args[1:]); err != nil {
				return err
			}
			if *batch < 1 || *batch > 100 || *owned <= 0 || *post <= 0 || *user <= 0 || *media < 0 {
				return fmt.Errorf("invalid batch size or prices")
			}
			c.LookupBatchSize = *batch
			c.OwnedPostMicrousd = *owned
			c.PostMicrousd = *post
			c.UserMicrousd = *user
			c.MediaMicrousd = *media
			c.OwnedEligible = *eligible
			if err = config.Save(configPath, c); err != nil {
				return err
			}
			return jsonOut(stdout, map[string]any{"saved": true})
		default:
			return fmt.Errorf("unknown config subcommand")
		}
	}
	if cmd == "db" && len(args) == 1 && args[0] == "init" {
		if err = os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
			return err
		}
		guard, e := lock.Acquire(dbPath + ".lock")
		if e != nil {
			return e
		}
		defer guard.Close()
		s, e := store.Open(dbPath)
		if e != nil {
			return e
		}
		if e = s.Close(); e != nil {
			return e
		}
		return jsonOut(stdout, map[string]any{"database_initialized": true})
	}
	switch cmd {
	case "db":
		if len(args) != 1 || args[0] != "check" {
			return fmt.Errorf("usage: db <init|check>")
		}
	case "sync", "export", "stats", "pending", "complete":
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	s, err := store.OpenExisting(dbPath)
	if err != nil {
		return fmt.Errorf("open existing database (verify XSTASH_HOME; use db init only for a new database): %w", err)
	}
	defer s.Close()
	switch cmd {
	case "db":
		if err := s.CheckIntegrity(); err != nil {
			return err
		}
		return jsonOut(stdout, map[string]any{"database_exists": true, "schema_version": 1, "quick_check": "ok", "foreign_key_check": "ok"})
	case "sync":
		return syncCommand(ctx, args, stdout, stderr, s, &c, configPath, dbPath)
	case "export":
		f := flags(cmd, stderr)
		format := f.String("format", "json", "json, markdown, csv")
		output := f.String("output", "-", "output file or -")
		kind := f.String("kind", "", "bookmark or like")
		since := f.String("since", "", "discovery date inclusive")
		until := f.String("until", "", "discovery date inclusive")
		refs := f.Bool("include-referenced", false, "include quotes")
		if err = f.Parse(args); err != nil {
			return err
		}
		for _, date := range []string{*since, *until} {
			if date != "" {
				if _, e := time.Parse("2006-01-02", date); e != nil {
					return fmt.Errorf("invalid date %q", date)
				}
			}
		}
		dataset, e := out.Collect(ctx, s, out.Options{Kind: *kind, Since: *since, Until: *until, IncludeReferenced: *refs})
		if e != nil {
			return e
		}
		if *output == "-" {
			return out.Write(stdout, *format, dataset)
		}
		file, e := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		e = out.Write(file, *format, dataset)
		closeErr := file.Close()
		if e != nil {
			return e
		}
		return closeErr
	case "pending":
		f := flags(cmd, stderr)
		purpose := f.String("purpose", "analysis", "processing purpose")
		limit := f.Int("limit", 100, "maximum posts")
		if err = f.Parse(args); err != nil {
			return err
		}
		items, e := s.Pending(ctx, *purpose, *limit)
		if e != nil {
			return e
		}
		return jsonOut(stdout, map[string]any{"purpose": *purpose, "items": items})
	case "complete":
		f := flags(cmd, stderr)
		purpose := f.String("purpose", "analysis", "processing purpose")
		id := f.String("id", "", "post ID")
		if err = f.Parse(args); err != nil {
			return err
		}
		if err = s.Complete(ctx, *id, *purpose, store.Now()); err != nil {
			return err
		}
		return jsonOut(stdout, map[string]any{"completed": *id, "purpose": *purpose})
	case "stats":
		return stats(ctx, s, stdout)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}
func syncCommand(ctx context.Context, args []string, stdout, stderr io.Writer, s *store.Store, c *config.Config, configPath, dbPath string) (err error) {
	f := flags("sync", stderr)
	kind := f.String("kind", "both", "bookmark, like, both")
	full := f.Bool("full", false, "ignore known boundaries and initial limits; max-pages still applies")
	size := f.Int("page-size", 5, "results per page")
	threshold := f.Int("known-threshold", 1, "consecutive known relations")
	maxNew := f.Int("max-new", 0, "new relations per kind, 0 for unlimited; partial runs do not advance the baseline")
	initialBookmarks := f.Int("initial-bookmarks", 100, "recent bookmarks in the first completed window, 0 for all")
	initialLikes := f.Int("initial-likes", 50, "recent likes in the first completed window, 0 for all")
	maxPages := f.Int("max-pages", 20, "pages per kind per run, 0 for unlimited; partial runs restart from the top")
	quoteDepth := f.Int("quote-depth", 1, "quote levels to resolve, 0..3")
	maxQuotes := f.Int("max-quote-lookups", 50, "missing quoted posts to look up per run, 0 for unlimited")
	metadata := f.Bool("metadata", false, "fetch missing author and media details (extra API cost)")
	maxMetadata := f.Int("max-metadata-lookups", 50, "posts to look up for metadata per run, 0 for unlimited")
	downloadMedia := f.Bool("media", false, "download available media files")
	if err = f.Parse(args); err != nil {
		return err
	}
	if *threshold < 1 || *maxNew < 0 || *size < 1 || *size > 100 || *initialBookmarks < 0 || *initialLikes < 0 || *maxPages < 0 || *quoteDepth < 0 || *quoteDepth > 3 || *maxQuotes < 0 || *maxMetadata < 0 {
		return fmt.Errorf("invalid sync options")
	}
	kinds := []string{}
	switch *kind {
	case "both":
		kinds = []string{"bookmark", "like"}
	case "bookmark", "like":
		kinds = []string{*kind}
	default:
		return fmt.Errorf("invalid kind")
	}
	if *size < 5 && (*kind == "both" || *kind == "like") {
		return fmt.Errorf("like page size must be at least 5")
	}
	if c.AccessToken == "" && c.RefreshToken == "" {
		return fmt.Errorf("authenticate first with config init")
	}
	if err = auth.Ready(*c); err != nil {
		return err
	}
	resolveMetadata := *metadata || *downloadMedia
	if resolveMetadata && c.MediaMicrousd <= 0 {
		return fmt.Errorf("media read price is unknown; set --media-microusd after checking Developer Console")
	}
	l, e := lock.Acquire(dbPath + ".lock")
	if e != nil {
		return e
	}
	defer l.Close()
	if c.AccessToken == "" || !c.ExpiresAt.IsZero() && time.Until(c.ExpiresAt) < 30*time.Second {
		if e = auth.Refresh(ctx, c, configPath); e != nil {
			return e
		}
	}
	var b [16]byte
	if _, e = rand.Read(b[:]); e != nil {
		return e
	}
	id := hex.EncodeToString(b[:])
	if e = s.StartRun(ctx, id, store.Now()); e != nil {
		return e
	}
	status := "failed"
	defer func() {
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		e := s.EndRun(context.Background(), id, status, detail, store.Now())
		if err == nil && e != nil {
			err = e
		}
	}()
	client := xapi.New(c.AccessToken)
	acct := syncer.Accountant{Store: s, Config: *c, RunID: id}
	acct.Attach(client)
	var authFailure error
	client.OnUnauthorized = func(ctx context.Context) (string, error) {
		if e := auth.Refresh(ctx, c, configPath); e != nil {
			authFailure = e
			return "", e
		}
		return c.AccessToken, nil
	}
	for _, k := range kinds {
		if _, e = s.DB.ExecContext(ctx, `INSERT INTO sync_resume(kind,full,reason,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind) DO UPDATE SET full=max(sync_resume.full,excluded.full),updated_at=excluded.updated_at`, k, *full, "pending", store.Now()); e != nil {
			return e
		}
	}
	if c.UserID == "" {
		c.UserID, e = syncer.UserID(ctx, client)
		if e != nil {
			return e
		}
		if e = config.Save(configPath, *c); e != nil {
			return e
		}
	}
	// The first identity lookup happens after the accountant is constructed.
	acct.Config.UserID = c.UserID
	if e = s.BindOwner(ctx, c.UserID); e != nil {
		return e
	}
	results := []syncer.Result{}
	var failures []error
	for _, k := range kinds {
		var resumeFull int
		if e = s.DB.QueryRowContext(ctx, `SELECT full FROM sync_resume WHERE kind=?`, k).Scan(&resumeFull); e != nil {
			return e
		}
		initialLimit := *initialBookmarks
		if k == "like" {
			initialLimit = *initialLikes
		}
		r, e := syncer.RunKind(ctx, s, client, c.UserID, k, syncer.Options{Full: resumeFull != 0, PageSize: *size, Threshold: *threshold, MaxNew: *maxNew, InitialLimit: initialLimit, MaxPages: *maxPages})
		results = append(results, r)
		if r.Complete && e == nil {
			if _, dbErr := s.DB.ExecContext(ctx, `DELETE FROM sync_resume WHERE kind=?`, k); dbErr != nil {
				return dbErr
			}
		} else {
			reason := r.Reason
			if e != nil {
				reason = e.Error()
			}
			if _, dbErr := s.DB.ExecContext(ctx, `UPDATE sync_resume SET reason=?,updated_at=? WHERE kind=?`, reason, store.Now(), k); dbErr != nil {
				return dbErr
			}
		}
		if _, dbErr := s.DB.ExecContext(ctx, `INSERT INTO sync_results(run_id,kind,new_relations,new_posts,retrieved,pages,reason) VALUES(?,?,?,?,?,?,?)`, id, k, r.New, r.NewPosts, r.Retrieved, r.Pages, r.Reason); dbErr != nil {
			return dbErr
		}
		if e != nil {
			failures = append(failures, e)
			break
		}
	}
	if authFailure != nil {
		return errors.Join(failures...)
	}
	if e = syncer.ResolveQuotes(ctx, s, client, syncer.QuoteOptions{Depth: *quoteDepth, MaxPosts: *maxQuotes, Metadata: resolveMetadata}); e != nil {
		failures = append(failures, e)
	}
	if authFailure != nil {
		return errors.Join(failures...)
	}
	if resolveMetadata {
		if e = syncer.ResolveMetadata(ctx, s, client, *maxMetadata); e != nil {
			failures = append(failures, e)
		}
	}
	if authFailure != nil {
		return errors.Join(failures...)
	}
	if *downloadMedia {
		if _, e = media.Download(ctx, s, filepath.Join(filepath.Dir(dbPath), "media")); e != nil {
			failures = append(failures, e)
		}
	}
	quotes, e := quoteCounts(ctx, s)
	if e != nil {
		return e
	}
	reasons, e := quoteReasons(ctx, s)
	if e != nil {
		return e
	}
	var pendingMetadata int
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM metadata_queue`).Scan(&pendingMetadata); e != nil {
		return e
	}
	metadataReasons, e := metadataCounts(ctx, s)
	if e != nil {
		return e
	}
	var requestCount, resourceCount int
	var estimated, uncertain int64
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(resources),0),coalesce(sum(estimated_microusd),0) FROM api_requests WHERE run_id=?`, id).Scan(&requestCount, &resourceCount, &estimated); e != nil {
		return e
	}
	if e = s.DB.QueryRowContext(ctx, `SELECT coalesce(sum(amount_microusd),0) FROM budget_reservations WHERE run_id=? AND state='reserved'`, id).Scan(&uncertain); e != nil {
		return e
	}
	month, e := currentMonth(ctx, s, time.Now())
	if e != nil {
		return e
	}
	var activeQuotes, deferredQuotes int
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM quote_queue WHERE status!='resolved' AND depth<=?`, *quoteDepth).Scan(&activeQuotes); e != nil {
		return e
	}
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM quote_queue WHERE status!='resolved' AND depth>?`, *quoteDepth).Scan(&deferredQuotes); e != nil {
		return e
	}
	if len(failures) == 0 {
		status = "completed"
		for _, result := range results {
			if !result.Complete {
				status = "partial"
				break
			}
		}
		if activeQuotes > 0 {
			status = "partial"
		}
		if resolveMetadata && pendingMetadata > 0 {
			status = "partial"
		}
	}
	if e = jsonOut(stdout, map[string]any{"run_id": id, "status": status, "results": results, "policy": map[string]any{"full_requested": *full, "max_new_per_kind": *maxNew, "initial_bookmarks": *initialBookmarks, "initial_likes": *initialLikes, "page_size": *size, "known_threshold": *threshold, "max_pages_per_kind": *maxPages, "quote_depth": *quoteDepth, "max_quote_lookups": *maxQuotes, "metadata_requested": resolveMetadata, "max_metadata_lookups": *maxMetadata, "monthly_spend_cap": false}, "deferred_quotes": deferredQuotes, "quotes": quotes, "quote_reasons": reasons, "pending_metadata": pendingMetadata, "metadata_reasons": metadataReasons, "requests": requestCount, "retrieved_resources": resourceCount, "estimated_microusd": estimated, "uncertain_reserved_microusd": uncertain, "currency": "USD", "current_month": month}); e != nil {
		return e
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	return nil
}
func currentMonth(ctx context.Context, s *store.Store, now time.Time) (map[string]any, error) {
	month := now.UTC().Format("2006-01")
	var req, res int
	var cost, uncertain int64
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(resources),0),coalesce(sum(estimated_microusd),0) FROM api_requests WHERE substr(requested_at,1,7)=?`, month).Scan(&req, &res, &cost); err != nil {
		return nil, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT coalesce(sum(amount_microusd),0) FROM budget_reservations WHERE state='reserved' AND substr(created_at,1,7)=?`, month).Scan(&uncertain); err != nil {
		return nil, err
	}
	details, err := s.CostDetails(ctx, month)
	if err != nil {
		return nil, err
	}
	return map[string]any{"cost_details": details, "month_utc": month, "requests": req, "resources": res, "estimated_microusd": cost, "estimated_usd": float64(cost) / 1000000, "uncertain_max_microusd": uncertain, "uncertain_max_usd": float64(uncertain) / 1000000, "actual_usd": nil, "basis": "historical local estimate at recorded rates; not repriced by current settings; excludes other clients and actual invoices"}, nil
}
func metadataCounts(ctx context.Context, s *store.Store) (map[string]int, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT reason,count(*) FROM metadata_queue GROUP BY reason`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var reason string
		var n int
		if e = rows.Scan(&reason, &n); e != nil {
			return nil, e
		}
		out[reason] = n
	}
	return out, rows.Err()
}
func quoteReasons(ctx context.Context, s *store.Store) ([]map[string]any, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT status,reason,count(*) FROM quote_queue WHERE status!='resolved' GROUP BY status,reason ORDER BY status,reason`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var status, reason string
		var count int
		if e = rows.Scan(&status, &reason, &count); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"status": status, "reason": reason, "count": count})
	}
	return out, rows.Err()
}
func quoteCounts(ctx context.Context, s *store.Store) (map[string]int, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT status,count(*) FROM quote_queue GROUP BY status`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if e = rows.Scan(&k, &n); e != nil {
			return nil, e
		}
		out[k] = n
	}
	return out, rows.Err()
}
func stats(ctx context.Context, s *store.Store, w io.Writer) error {
	quotes, e := quoteCounts(ctx, s)
	if e != nil {
		return e
	}
	daily := []map[string]any{}
	rows, e := s.DB.QueryContext(ctx, `SELECT substr(requested_at,1,10),count(*),sum(resources),sum(estimated_microusd) FROM api_requests GROUP BY 1 ORDER BY 1`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var date string
		var req, res int
		var cost int64
		if e = rows.Scan(&date, &req, &res, &cost); e != nil {
			rows.Close()
			return e
		}
		daily = append(daily, map[string]any{"date_utc": date, "requests": req, "resources": res, "estimated_microusd": cost})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	runs := []map[string]any{}
	rows, e = s.DB.QueryContext(ctx, `SELECT r.id,r.started_at,r.status,coalesce((SELECT sum(new_relations) FROM sync_results WHERE run_id=r.id),0),coalesce((SELECT sum(new_posts) FROM sync_results WHERE run_id=r.id),0),coalesce((SELECT count(*) FROM api_requests WHERE run_id=r.id),0),coalesce((SELECT sum(resources) FROM api_requests WHERE run_id=r.id),0),coalesce((SELECT sum(estimated_microusd) FROM api_requests WHERE run_id=r.id),0) FROM sync_runs r ORDER BY r.started_at DESC LIMIT 100`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, at, status string
		var added, posts, requests, resources int
		var cost int64
		if e = rows.Scan(&id, &at, &status, &added, &posts, &requests, &resources, &cost); e != nil {
			rows.Close()
			return e
		}
		runs = append(runs, map[string]any{"id": id, "started_at": at, "status": status, "new_relations": added, "new_posts": posts, "requests": requests, "resources": resources, "estimated_microusd": cost})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	monthly := []map[string]any{}
	rows, e = s.DB.QueryContext(ctx, `SELECT substr(requested_at,1,7),count(*),sum(resources),sum(estimated_microusd) FROM api_requests GROUP BY 1 ORDER BY 1`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var month string
		var req, res int
		var cost int64
		if e = rows.Scan(&month, &req, &res, &cost); e != nil {
			rows.Close()
			return e
		}
		monthly = append(monthly, map[string]any{"month_utc": month, "requests": req, "resources": res, "estimated_microusd": cost})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	var reserved int64
	if e = s.DB.QueryRowContext(ctx, `SELECT coalesce(sum(amount_microusd),0) FROM budget_reservations WHERE state='reserved'`).Scan(&reserved); e != nil {
		return e
	}
	metadata, e := metadataCounts(ctx, s)
	if e != nil {
		return e
	}
	pending := []map[string]any{}
	rows, e = s.DB.QueryContext(ctx, `SELECT kind,full,reason,updated_at FROM sync_resume ORDER BY kind`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var kind, reason, at string
		var full int
		if e = rows.Scan(&kind, &full, &reason, &at); e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, map[string]any{"kind": kind, "full": full != 0, "reason": reason, "updated_at": at})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	month, e := currentMonth(ctx, s, time.Now())
	if e != nil {
		return e
	}
	return jsonOut(w, map[string]any{"daily": daily, "monthly": monthly, "current_month": month, "runs": runs, "quotes": quotes, "metadata": metadata, "pending_sync": pending, "uncertain_reserved_microusd": reserved, "currency": "USD", "amounts": "estimated, not actual charges"})
}
