package logging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolve_FlagBeatsEnvAndManifest(t *testing.T) {
	res, err := Resolve(ResolveOpts{
		FlagDir:        "/explicit/logs",
		ManifestDir:    "/wt",
		ManifestLogDir: "/wt/logs",
		EnvLookup: func(k string) string {
			if k == EnvDir {
				return "/env/logs"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceFlag {
		t.Errorf("source: got %q, want flag", res.Source)
	}
	if !filepath.IsAbs(res.Dir) {
		t.Errorf("dir: want absolute, got %q", res.Dir)
	}
	if !strings.HasSuffix(res.Dir, filepath.FromSlash("/explicit/logs")) {
		t.Errorf("dir: got %q, want /explicit/logs", res.Dir)
	}
}

func TestResolve_EnvBeatsManifest(t *testing.T) {
	res, err := Resolve(ResolveOpts{
		ManifestDir: "/wt",
		EnvLookup: func(k string) string {
			if k == EnvDir {
				return "/env/logs"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceEnv {
		t.Errorf("source: got %q, want env", res.Source)
	}
	if !strings.HasSuffix(res.Dir, filepath.FromSlash("/env/logs")) {
		t.Errorf("dir: got %q", res.Dir)
	}
}

func TestResolve_ManifestExplicitLogDir(t *testing.T) {
	res, err := Resolve(ResolveOpts{
		ManifestDir:    "/wt",
		ManifestLogDir: "custom-logs",
		EnvLookup:      func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceWorktree {
		t.Errorf("source: got %q, want worktree", res.Source)
	}
	want := filepath.Clean("/wt/custom-logs")
	if got := filepath.Clean(res.Dir); got != want {
		t.Errorf("dir: got %q, want %q", got, want)
	}
}

func TestResolve_ManifestSynthesisedDefault(t *testing.T) {
	res, err := Resolve(ResolveOpts{
		ManifestDir: "/wt",
		EnvLookup:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceWorktree {
		t.Errorf("source: got %q, want worktree", res.Source)
	}
	want := filepath.Clean("/wt/.bacio/logs")
	if got := filepath.Clean(res.Dir); got != want {
		t.Errorf("dir: got %q, want %q", got, want)
	}
}

func TestResolve_DefaultHomeFallback(t *testing.T) {
	res, err := Resolve(ResolveOpts{
		HomeDir:   "/h",
		EnvLookup: func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceDefault {
		t.Errorf("source: got %q, want default", res.Source)
	}
	want := filepath.Clean("/h/.bacio/logs")
	if got := filepath.Clean(res.Dir); got != want {
		t.Errorf("dir: got %q, want %q", got, want)
	}
}

func TestResolve_LevelFlagBeatsEnv(t *testing.T) {
	res, err := Resolve(ResolveOpts{
		HomeDir:   "/h",
		FlagLevel: "debug",
		EnvLookup: func(k string) string {
			if k == EnvLevel {
				return "error"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Level != slog.LevelDebug {
		t.Errorf("level: got %v, want debug", res.Level)
	}
	if res.LevelSource != SourceFlag {
		t.Errorf("level source: got %q, want flag", res.LevelSource)
	}
}

func TestResolve_LevelEnvOnly(t *testing.T) {
	res, err := Resolve(ResolveOpts{
		HomeDir: "/h",
		EnvLookup: func(k string) string {
			if k == EnvLevel {
				return "WARN"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Level != slog.LevelWarn {
		t.Errorf("level: got %v, want warn", res.Level)
	}
	if res.LevelSource != SourceEnv {
		t.Errorf("level source: got %q, want env", res.LevelSource)
	}
}

func TestResolve_LevelDefault(t *testing.T) {
	res, err := Resolve(ResolveOpts{
		HomeDir:   "/h",
		EnvLookup: func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Level != DefaultLevel {
		t.Errorf("level: got %v, want %v", res.Level, DefaultLevel)
	}
	if res.LevelSource != SourceDefault {
		t.Errorf("level source: got %q, want default", res.LevelSource)
	}
}

func TestResolve_LevelInvalid(t *testing.T) {
	_, err := Resolve(ResolveOpts{
		HomeDir:   "/h",
		FlagLevel: "loud",
		EnvLookup: func(string) string { return "" },
	})
	if err == nil {
		t.Fatal("expected error for invalid level, got nil")
	}
	if !strings.Contains(err.Error(), "loud") {
		t.Errorf("err: want mention of bad value, got %v", err)
	}
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"Debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"  Info  ", slog.LevelInfo},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if err != nil {
			t.Errorf("ParseLevel(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseLevel(%q): got %v, want %v", c.in, got, c.want)
		}
	}
	// Bad values fail loud.
	if _, err := ParseLevel("warning"); err == nil {
		t.Errorf("ParseLevel(\"warning\"): want error, got nil")
	}
	if _, err := ParseLevel(""); err == nil {
		t.Errorf("ParseLevel(\"\"): want error, got nil")
	}
}

func TestFilename(t *testing.T) {
	t1 := time.Date(2026, 5, 19, 9, 30, 0, 0, time.Local)
	got := Filename("api", t1)
	want := "bacio-api-2026-05-19.log"
	if got != want {
		t.Errorf("Filename: got %q, want %q", got, want)
	}
}

func TestNewHandler_WritesToFileAndStderr(t *testing.T) {
	dir := t.TempDir()
	var stderrBuf bytes.Buffer
	now := time.Date(2026, 5, 19, 9, 30, 0, 0, time.Local)

	h, err := NewHandler(NewHandlerOpts{
		Dir:       dir,
		Component: "api",
		Level:     slog.LevelInfo,
		Stderr:    &stderrBuf,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer h.Close()

	logger := slog.New(h)
	logger.Info("hello", "key", "value")

	// stderr sink captured the line.
	if !strings.Contains(stderrBuf.String(), `msg=hello`) {
		t.Errorf("stderr: want hello, got %q", stderrBuf.String())
	}

	// file sink captured the same content.
	want := filepath.Join(dir, "bacio-api-2026-05-19.log")
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(body), `msg=hello`) {
		t.Errorf("file: want hello, got %q", string(body))
	}
	if !strings.Contains(string(body), `key=value`) {
		t.Errorf("file: want key=value, got %q", string(body))
	}
}

func TestNewHandler_LevelFilters(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 19, 9, 30, 0, 0, time.Local)
	h, err := NewHandler(NewHandlerOpts{
		Dir:       dir,
		Component: "api",
		Level:     slog.LevelWarn,
		Stderr:    io.Discard,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer h.Close()
	logger := slog.New(h)
	logger.Info("not-shown")
	logger.Warn("shown")
	body, _ := os.ReadFile(filepath.Join(dir, "bacio-api-2026-05-19.log"))
	if strings.Contains(string(body), "not-shown") {
		t.Errorf("info line leaked past warn threshold: %q", string(body))
	}
	if !strings.Contains(string(body), "shown") {
		t.Errorf("warn line missing: %q", string(body))
	}
}

func TestNewHandler_DailyRotation(t *testing.T) {
	dir := t.TempDir()
	var clock atomic.Int64
	clock.Store(time.Date(2026, 5, 19, 23, 59, 59, 0, time.Local).UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()) }

	h, err := NewHandler(NewHandlerOpts{
		Dir:       dir,
		Component: "api",
		Level:     slog.LevelInfo,
		Stderr:    io.Discard,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer h.Close()

	logger := slog.New(h)
	logger.Info("first-day")

	// Advance the clock past midnight (local time) and emit again.
	clock.Store(time.Date(2026, 5, 20, 0, 0, 1, 0, time.Local).UnixNano())
	logger.Info("second-day")

	day1 := filepath.Join(dir, "bacio-api-2026-05-19.log")
	day2 := filepath.Join(dir, "bacio-api-2026-05-20.log")

	b1, err := os.ReadFile(day1)
	if err != nil {
		t.Fatalf("read day1: %v", err)
	}
	if !strings.Contains(string(b1), "first-day") {
		t.Errorf("day1: want first-day, got %q", string(b1))
	}
	if strings.Contains(string(b1), "second-day") {
		t.Errorf("day1: must not contain second-day, got %q", string(b1))
	}
	b2, err := os.ReadFile(day2)
	if err != nil {
		t.Fatalf("read day2: %v", err)
	}
	if !strings.Contains(string(b2), "second-day") {
		t.Errorf("day2: want second-day, got %q", string(b2))
	}
}

func TestNewHandler_CreatesDirOnDemand(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "nested", "deeper", "logs")
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("precondition: dir already exists")
	}
	h, err := NewHandler(NewHandlerOpts{
		Dir:       dir,
		Component: "api",
		Level:     slog.LevelInfo,
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer h.Close()
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}

func TestNewHandler_MkdirFailureBubblesUp(t *testing.T) {
	base := t.TempDir()
	// Make a file where the dir should be — MkdirAll will reject it.
	conflict := filepath.Join(base, "conflict")
	if err := os.WriteFile(conflict, []byte("oops"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	dir := filepath.Join(conflict, "logs")
	_, err := NewHandler(NewHandlerOpts{
		Dir:       dir,
		Component: "api",
		Level:     slog.LevelInfo,
		Stderr:    io.Discard,
	})
	if err == nil {
		t.Fatal("expected error when MkdirAll fails, got nil")
	}
}

func TestNewHandler_WithAttrsForwardsToBothSinks(t *testing.T) {
	dir := t.TempDir()
	var stderrBuf bytes.Buffer
	now := time.Date(2026, 5, 19, 9, 30, 0, 0, time.Local)

	h, err := NewHandler(NewHandlerOpts{
		Dir:       dir,
		Component: "api",
		Level:     slog.LevelInfo,
		Stderr:    &stderrBuf,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer h.Close()
	logger := slog.New(h).With("component", "api")
	logger.Info("hi")
	if !strings.Contains(stderrBuf.String(), "component=api") {
		t.Errorf("stderr: want component=api, got %q", stderrBuf.String())
	}
	body, _ := os.ReadFile(filepath.Join(dir, "bacio-api-2026-05-19.log"))
	if !strings.Contains(string(body), "component=api") {
		t.Errorf("file: want component=api, got %q", string(body))
	}
}

func TestStderrOnlyHandler(t *testing.T) {
	h := StderrOnlyHandler(slog.LevelInfo)
	if h == nil {
		t.Fatal("nil handler")
	}
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Errorf("info not enabled")
	}
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Errorf("debug enabled but should be filtered")
	}
}

func TestNewHandler_AppendsAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 19, 9, 30, 0, 0, time.Local)
	h1, err := NewHandler(NewHandlerOpts{
		Dir:       dir,
		Component: "api",
		Level:     slog.LevelInfo,
		Stderr:    io.Discard,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("first NewHandler: %v", err)
	}
	slog.New(h1).Info("first-line")
	if err := h1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen the SAME file — second handler appends, doesn't truncate.
	h2, err := NewHandler(NewHandlerOpts{
		Dir:       dir,
		Component: "api",
		Level:     slog.LevelInfo,
		Stderr:    io.Discard,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("second NewHandler: %v", err)
	}
	slog.New(h2).Info("second-line")
	h2.Close()

	body, err := os.ReadFile(filepath.Join(dir, "bacio-api-2026-05-19.log"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "first-line") {
		t.Errorf("first-line missing: %q", string(body))
	}
	if !strings.Contains(string(body), "second-line") {
		t.Errorf("second-line missing: %q", string(body))
	}
}
