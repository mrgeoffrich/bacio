package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

// TestParseDesktopFlags_DefaultsEmpty asserts the desktop's tiny flag
// surface defaults to empty values (everything optional), so a no-arg
// launch falls through to the wtenv resolver's default branch.
func TestParseDesktopFlags_DefaultsEmpty(t *testing.T) {
	got := parseDesktopFlags(nil)
	if got.DBPath != "" || got.EnvPath != "" {
		t.Errorf("defaults: %+v", got)
	}
}

func TestParseDesktopFlags_PicksUpEnv(t *testing.T) {
	got := parseDesktopFlags([]string{"--env", "/path/to/env.yaml", "--db", "/explicit.sqlite"})
	if got.EnvPath != "/path/to/env.yaml" {
		t.Errorf("env: got %q", got.EnvPath)
	}
	if got.DBPath != "/explicit.sqlite" {
		t.Errorf("db: got %q", got.DBPath)
	}
}

// TestResolveDesktopEnv_DBFlagOverridesEverything checks that --db
// takes precedence over the BACIO_ENV env var.
func TestResolveDesktopEnv_DBFlagOverridesEverything(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("BACIO_ENV", filepath.Join(tmp, "missing.yaml"))
	res, err := resolveDesktopEnv(tmp, desktopFlags{DBPath: "/explicit.sqlite"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.DBPath != "/explicit.sqlite" {
		t.Errorf("dbpath: got %q", res.DBPath)
	}
	if res.Source != wtenv.SourceFlag {
		t.Errorf("source: got %q want flag", res.Source)
	}
}

// TestResolveDesktopEnv_EnvFlagOverridesEnvVar checks the --env flag
// path takes precedence over $BACIO_ENV.
func TestResolveDesktopEnv_EnvFlagOverridesEnvVar(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	envPath := filepath.Join(tmp, "from-flag.yaml")
	if err := os.WriteFile(envPath, []byte("identity:\n  slug: flag\nallocations:\n  api_port: 5555\n  db_path: /flag.sqlite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BACIO_ENV", "/ignored.yaml")
	res, err := resolveDesktopEnv(tmp, desktopFlags{EnvPath: envPath})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.DBPath != "/flag.sqlite" {
		t.Errorf("dbpath: got %q", res.DBPath)
	}
	if res.APIAddr != "127.0.0.1:5555" {
		t.Errorf("addr: got %q", res.APIAddr)
	}
	if res.Source != wtenv.SourceEnv {
		t.Errorf("source: got %q", res.Source)
	}
}
