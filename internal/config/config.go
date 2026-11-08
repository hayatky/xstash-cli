package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	ClientID          string    `json:"client_id"`
	ClientSecret      string    `json:"client_secret,omitempty"`
	AccessToken       string    `json:"access_token,omitempty"`
	RefreshToken      string    `json:"refresh_token,omitempty"`
	TokenType         string    `json:"token_type,omitempty"`
	Scope             string    `json:"scope,omitempty"`
	ScopeSource       string    `json:"scope_source,omitempty"`
	RefreshState      string    `json:"refresh_state,omitempty"`
	RefreshStartedAt  time.Time `json:"refresh_started_at,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitempty"`
	UserID            string    `json:"user_id,omitempty"`
	LookupBatchSize   int       `json:"lookup_batch_size"`
	OwnedPostMicrousd int64     `json:"owned_post_microusd"`
	PostMicrousd      int64     `json:"post_microusd"`
	UserMicrousd      int64     `json:"user_microusd"`
	MediaMicrousd     int64     `json:"media_microusd"`
	OwnedEligible     bool      `json:"owned_eligible"`
}

func Default() Config {
	return Config{LookupBatchSize: 5, PostMicrousd: 5000, UserMicrousd: 10000, OwnedPostMicrousd: 1000}
}
func Paths() (string, string, error) {
	if p := os.Getenv("XSTASH_HOME"); p != "" {
		return filepath.Join(p, "config.json"), filepath.Join(p, "xstash-go.sqlite"), nil
	}
	c, e := os.UserConfigDir()
	if e != nil {
		return "", "", e
	}
	d, e := os.UserHomeDir()
	if e != nil {
		return "", "", e
	}
	return filepath.Join(c, "xstash-go", "config.json"), filepath.Join(d, ".xstash-go", "xstash-go.sqlite"), nil
}
func Load(path string) (Config, error) {
	c := Default()
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, fmt.Errorf("invalid config: %w", e)
	}
	return c, nil
}
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// ApplyEnv accepts app settings, but never replaces durable token state. Old
// token overrides must be removed explicitly instead of rolling back rotation.
func ApplyEnv(c *Config, getenv func(string) string) error {
	if getenv("XSTASH_ACCESS_TOKEN") != "" || getenv("XSTASH_REFRESH_TOKEN") != "" {
		return fmt.Errorf("token environment overrides are no longer supported; unset XSTASH_ACCESS_TOKEN and XSTASH_REFRESH_TOKEN and use config init if authentication is needed")
	}
	if id := getenv("XSTASH_CLIENT_ID"); id != "" && id != c.ClientID {
		if c.AccessToken != "" || c.RefreshToken != "" || c.RefreshState != "" {
			return fmt.Errorf("XSTASH_CLIENT_ID conflicts with saved authentication; unset it and use config init --client-id to change apps")
		}
		c.ClientID = id
		c.ClientSecret = ""
		c.AccessToken = ""
		c.RefreshToken = ""
		c.ExpiresAt = time.Time{}
		c.TokenType = ""
		c.Scope = ""
		c.ScopeSource = ""
		c.UserID = ""
		c.OwnedEligible = false
	}
	if v := getenv("XSTASH_CLIENT_SECRET"); v != "" {
		c.ClientSecret = v
	}
	return nil
}
