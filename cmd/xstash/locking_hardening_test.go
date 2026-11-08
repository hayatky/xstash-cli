package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hayatky/xstash-cli/internal/lock"
)

func TestMutatingCommandsLockBeforeLoad(t *testing.T) {
	for _, args := range [][]string{{"config", "set"}, {"config", "init"}, {"sync"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XSTASH_HOME", dir)
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, []byte("invalid JSON"), 0600); err != nil {
				t.Fatal(err)
			}
			guard, err := lock.Acquire(path + ".lock")
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			var out, errout bytes.Buffer
			err = run(context.Background(), args, &out, &errout)
			if err == nil || !strings.Contains(err.Error(), "another sync") {
				t.Fatalf("did not acquire lock before load: %v", err)
			}
		})
	}
}
