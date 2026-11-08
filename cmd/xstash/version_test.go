package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionAndHelpIgnoreLocalState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("XSTASH_HOME", home)
	t.Setenv("XSTASH_ACCESS_TOKEN", "invalid-test-override")
	for _, arg := range []string{"version", "--version", "-v", "help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := run(context.Background(), []string{arg}, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", &stderr)
			}
			if strings.Contains(arg, "version") || arg == "-v" {
				if stdout.String() != "xstash "+version+" ("+commit+")\n" {
					t.Fatalf("version: %q", stdout.String())
				}
			} else if !strings.Contains(stdout.String(), "Usage: xstash") {
				t.Fatalf("help: %q", stdout.String())
			}
		})
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("informational commands created state: %v", err)
	}
}
