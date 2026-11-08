package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/hayatky/xstash-cli/internal/auth"
	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/secretinput"
)

func importCommand(ctx context.Context, args []string, stdout, stderr io.Writer, c *config.Config, path string) (err error) {
	// Flag parsing errors may echo supplied values. Suppress them so even a
	// mistakenly supplied token flag/argument cannot be reproduced in logs.
	f := flags("config import", io.Discard)
	id := f.String("client-id", c.ClientID, "public OAuth client ID")
	scope := f.String("scopes", "", "exact selected scopes; required")
	expires := f.String("expires-at", "", "issuer's absolute RFC3339 expiry; omitted means refresh before use")
	replace := f.Bool("replace", false, "replace existing credentials with a newly issued grant")
	if e := f.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			f.SetOutput(stderr)
			fmt.Fprintln(stderr, "Usage: xstash config import --client-id ID --scopes 'tweet.read users.read bookmark.read like.read offline.access' [--expires-at RFC3339] [--replace]")
			f.PrintDefaults()
			return nil
		}
		return fmt.Errorf("invalid config import options; use config import --help; never pass tokens as arguments")
	}
	if f.NArg() != 0 {
		return fmt.Errorf("config import accepts no positional arguments; enter tokens only at the hidden prompts")
	}
	opts := auth.ImportOptions{ClientID: *id, Scope: *scope, Replace: *replace}
	if *expires != "" {
		opts.ExpiresAt, err = time.Parse(time.RFC3339, *expires)
		if err != nil {
			return fmt.Errorf("--expires-at must be the issuer's absolute RFC3339 expiry")
		}
	}
	if err = auth.CheckImport(*c, path, opts); err != nil {
		return err
	}
	terminal, err := secretinput.Open(ctx)
	if err != nil {
		return err
	}
	// Restore echo before reporting success. Ordinary cancellations keep the
	// old config. A process kill during atomic save leaves the old or new file.
	err = func() (result error) {
		defer func() { result = errors.Join(result, terminal.Close()) }()
		return auth.Import(terminal.Context(), c, path, opts, terminal.Prompt)
	}()
	if err != nil {
		return err
	}
	return jsonOut(stdout, map[string]any{"credentials_imported": true, "verified_with_x": false, "scope_source": c.ScopeSource, "expires_at": c.ExpiresAt, "refresh_required": time.Until(c.ExpiresAt) < 30*time.Second})
}
