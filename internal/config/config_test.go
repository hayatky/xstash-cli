package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvTokensCannotReplaceDurableState(t *testing.T) {
	for _, key := range []string{"XSTASH_ACCESS_TOKEN", "XSTASH_REFRESH_TOKEN"} {
		t.Run(key, func(t *testing.T) {
			c := Config{ClientID: "app", AccessToken: "rotated", RefreshToken: "rotated-refresh", UserID: "1", RefreshState: "pending", Scope: "saved"}
			before := c
			err := ApplyEnv(&c, func(k string) string {
				if k == key {
					return "stale-token"
				}
				return ""
			})
			if err == nil || c != before {
				t.Fatal("environment override changed durable auth state")
			}
		})
	}
}
func TestEnvClientChangeRejectsSavedAuthentication(t *testing.T) {
	c := Config{ClientID: "old", ClientSecret: "secret", AccessToken: "token", RefreshState: "pending"}
	before := c
	err := ApplyEnv(&c, func(k string) string {
		if k == "XSTASH_CLIENT_ID" {
			return "new"
		}
		return ""
	})
	if err == nil || c != before {
		t.Fatal("app override changed durable auth state")
	}
}
func TestEnvClientSettingsWithoutSavedAuthentication(t *testing.T) {
	c := Default()
	err := ApplyEnv(&c, func(k string) string {
		return map[string]string{"XSTASH_CLIENT_ID": "app", "XSTASH_CLIENT_SECRET": "secret"}[k]
	})
	if err != nil || c.ClientID != "app" || c.ClientSecret != "secret" {
		t.Fatal("app setup failed")
	}
}

func TestLegacyBudgetIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"budget_microusd":2000000,"cycle_day":12,"post_microusd":5000}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.LookupBatchSize != 5 || c.PostMicrousd != 5000 {
		t.Fatalf("legacy config lost defaults: %+v", c)
	}
}
