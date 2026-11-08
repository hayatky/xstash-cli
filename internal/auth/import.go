package auth

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hayatky/xstash-cli/internal/config"
)

// ImportOptions describes user-asserted metadata, not a token endpoint response.
// ExpiresAt is an absolute expiry from the issuer. Zero means unknown/expired.
type ImportOptions struct {
	ClientID  string
	Scope     string
	ExpiresAt time.Time
	Replace   bool
}

// CheckImport runs before reading secrets. Callers must hold the config lock
// from config.Load through Import, just as for Login and Refresh.
func CheckImport(c config.Config, path string, opts ImportOptions) error {
	if !validImportedToken([]byte(opts.ClientID)) {
		return fmt.Errorf("a public OAuth client ID is required")
	}
	if c.ClientSecret != "" {
		return fmt.Errorf("config import supports public clients only; use config init for confidential clients")
	}
	if err := validateScopes(opts.Scope); err != nil {
		return fmt.Errorf("--scopes must contain exactly the five read/offline scopes selected when issuing the grant")
	}
	if !opts.Replace && (c.AccessToken != "" || c.RefreshToken != "" || c.RefreshState != "") {
		return fmt.Errorf("saved credentials already exist; use --replace only for a newly issued grant, never to restore an old refresh token")
	}
	st, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot inspect configuration destination")
	}
	if err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("configuration destination must be a regular file, not a symbolic link")
	}
	return nil
}

// Import saves a manually issued public-client grant without any network call.
// prompt must read exclusively from a hidden terminal, not argv, env or stdin.
func Import(ctx context.Context, c *config.Config, path string, opts ImportOptions, prompt func(context.Context, string) ([]byte, error)) error {
	if err := CheckImport(*c, path, opts); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	access, err := prompt(ctx, "OAuth 2.0 user access token (hidden): ")
	defer clear(access)
	if err != nil {
		return fmt.Errorf("token import cancelled or terminal input failed; credentials were not changed")
	}
	if !validImportedToken(access) {
		return fmt.Errorf("invalid access token input; expected one nonempty ASCII token without whitespace")
	}
	refresh, err := prompt(ctx, "OAuth 2.0 refresh token (hidden): ")
	defer clear(refresh)
	if err != nil {
		return fmt.Errorf("token import cancelled or terminal input failed; credentials were not changed")
	}
	if !validImportedToken(refresh) {
		return fmt.Errorf("invalid refresh token input; expected one nonempty ASCII token without whitespace")
	}
	// A possibly consumed refresh token must not clear a pending refresh marker.
	// Reject repeats even outside pending state. Older rotated copies cannot be
	// detected offline, so the user must supply a newly issued grant.
	if string(refresh) == c.RefreshToken {
		return fmt.Errorf("the refresh token matches the saved token; import a newly issued grant instead")
	}
	next := *c
	next.ClientID, next.ClientSecret = opts.ClientID, ""
	next.AccessToken, next.RefreshToken = string(access), string(refresh)
	next.TokenType = "Bearer"
	next.Scope, next.ScopeSource = strings.Join(strings.Fields(opts.Scope), " "), "user_selected"
	next.ExpiresAt = opts.ExpiresAt.UTC()
	if next.ExpiresAt.IsZero() {
		next.ExpiresAt = time.Now().UTC()
	} // Unknown is never treated as unlimited.
	next.RefreshState, next.RefreshStartedAt = "", time.Time{}
	next.UserID, next.OwnedEligible = "", false
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("token import cancelled; credentials were not changed")
	}
	if err := config.Save(path, next); err != nil {
		// Rename may have succeeded before directory fsync failed. Do not invite
		// a blind retry or print wrapped errors that may contain sensitive input.
		return fmt.Errorf("could not confirm durable credential save; it may already have completed; inspect config show before retrying")
	}
	*c = next
	return nil
}

func validImportedToken(b []byte) bool {
	if len(b) == 0 || len(b) > 16*1024 {
		return false
	}
	for _, c := range b {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
