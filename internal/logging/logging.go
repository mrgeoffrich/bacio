// Package logging resolves an opt-in file-logging destination for the
// long-running bacio processes (`bacio api`, `bacio channel`, the
// desktop binary, and any future server-shaped command) and builds the
// slog.Handler that fans every log event out to BOTH stderr and the
// resolved per-day log file.
//
// Short-lived CLI verbs (e.g. `bacio issue list`) deliberately do NOT
// open a file sink — they'd bloat the log dir for no operational
// benefit. The flags + env vars defined here are persistent so they
// stay reachable from the harness-integration shims, but a verb that
// hasn't been opted in keeps its existing stderr-only behaviour.
//
// Resolution order for the log directory, mirroring the BACI-63 wtenv
// resolver (highest precedence first):
//
//  1. Explicit --log-dir <path> flag.
//  2. $BACIO_LOG_DIR env var.
//  3. Worktree manifest at <worktree-root>/environment-config.yaml,
//     reading the optional allocations.log_dir field. When absent the
//     resolver synthesises <worktree-root>/.bacio/logs/ — siblings the
//     existing .bacio/db.sqlite location, so the same gitignore rule
//     covers it.
//  4. Default fallback: ~/.bacio/logs/.
//
// Directory creation is on demand (mkdir -p). When creation fails, the
// caller emits ONE warning to stderr and continues without file
// logging — the "never block the process from starting" rule from the
// BACI-73 brief.
package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EnvDir names the environment variable that points at an explicit log
// directory. Resolves after the flag, before the worktree manifest.
const EnvDir = "BACIO_LOG_DIR"

// EnvLevel names the env var that overrides the default log level.
const EnvLevel = "BACIO_LOG_LEVEL"

// DefaultLevel is the level applied when neither --log-level nor
// $BACIO_LOG_LEVEL is set.
const DefaultLevel = slog.LevelInfo

// Source identifies which step of the resolution chain produced the
// log directory. Surfaced on `bacio status` so an operator can see at
// a glance which knob is in effect.
type Source string

const (
	// SourceFlag means an explicit --log-dir flag was supplied.
	SourceFlag Source = "flag"
	// SourceEnv means $BACIO_LOG_DIR was set.
	SourceEnv Source = "env"
	// SourceWorktree means a worktree manifest fed the log dir —
	// either explicitly via allocations.log_dir, or implicitly
	// via the synthesised <worktree-root>/.bacio/logs/ fallback.
	SourceWorktree Source = "worktree"
	// SourceDefault means we fell through to ~/.bacio/logs/.
	SourceDefault Source = "default"
)

// Resolved is the output of Resolve. Dir is always populated; Level
// is the parsed slog.Level (DefaultLevel when nothing overrode it).
type Resolved struct {
	Source Source
	Dir    string
	Level  slog.Level
	// LevelSource records which knob set Level. Useful for diagnostics
	// surfaced on `bacio status`.
	LevelSource Source
}

// ResolveOpts feeds Resolve. Every override is optional — empty
// strings let the resolver pick the next-precedence value.
type ResolveOpts struct {
	// FlagDir / FlagLevel are the raw values supplied on the command
	// line (or any other "explicit caller override" channel). Empty
	// strings opt out so the env / manifest / default chain runs.
	FlagDir   string
	FlagLevel string

	// EnvLookup defaults to os.Getenv. Tests pass a fake to control the
	// environment without exporting real vars.
	EnvLookup func(string) string

	// HomeDir overrides ~ — used by tests so the default fallback can be
	// pinned to a tempdir.
	HomeDir string

	// ManifestDir is the worktree manifest's containing directory.
	// Empty when no manifest fed the wtenv resolution chain (i.e. the
	// process is running under the legacy default). When non-empty the
	// resolver uses it both to synthesise the default
	// <manifest-dir>/.bacio/logs/ path AND to anchor a relative
	// ManifestLogDir.
	ManifestDir string

	// ManifestLogDir is the value of allocations.log_dir from the
	// resolved worktree manifest, if any. Absolute paths are honoured
	// as-is; relative paths resolve against ManifestDir. Empty falls
	// back to the <ManifestDir>/.bacio/logs/ synthesised default.
	ManifestLogDir string
}

// Resolve walks the precedence chain and produces the destination dir
// plus the configured slog.Level. Bad level strings (anything other
// than the four canonical names, case-insensitive) error — better to
// fail loudly on startup than silently degrade to info.
func Resolve(opts ResolveOpts) (Resolved, error) {
	if opts.EnvLookup == nil {
		opts.EnvLookup = os.Getenv
	}

	level, levelSrc, err := resolveLevel(opts)
	if err != nil {
		return Resolved{}, err
	}

	res := Resolved{
		Level:       level,
		LevelSource: levelSrc,
	}

	// Step 1: explicit --log-dir flag wins.
	if dir := strings.TrimSpace(opts.FlagDir); dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return Resolved{}, fmt.Errorf("resolve --log-dir %q: %w", dir, err)
		}
		res.Source = SourceFlag
		res.Dir = abs
		return res, nil
	}

	// Step 2: $BACIO_LOG_DIR.
	if envDir := strings.TrimSpace(opts.EnvLookup(EnvDir)); envDir != "" {
		abs, err := filepath.Abs(envDir)
		if err != nil {
			return Resolved{}, fmt.Errorf("resolve %s=%q: %w", EnvDir, envDir, err)
		}
		res.Source = SourceEnv
		res.Dir = abs
		return res, nil
	}

	// Step 3: worktree manifest. Honour the explicit allocations.log_dir
	// field; otherwise synthesise <manifest-dir>/.bacio/logs/.
	if opts.ManifestDir != "" {
		dir := strings.TrimSpace(opts.ManifestLogDir)
		if dir == "" {
			dir = filepath.Join(".bacio", "logs")
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(opts.ManifestDir, dir)
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return Resolved{}, fmt.Errorf("resolve manifest log_dir %q: %w", dir, err)
		}
		res.Source = SourceWorktree
		res.Dir = abs
		return res, nil
	}

	// Step 4: default. ~/.bacio/logs/.
	home := opts.HomeDir
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Resolved{}, fmt.Errorf("resolve home dir: %w", err)
		}
		home = h
	}
	res.Source = SourceDefault
	res.Dir = filepath.Join(home, ".bacio", "logs")
	return res, nil
}

// resolveLevel parses the level knob. Flag wins over env; bad values
// fail loud. Case-insensitive (`debug`, `DEBUG`, `Debug` all accepted).
func resolveLevel(opts ResolveOpts) (slog.Level, Source, error) {
	if raw := strings.TrimSpace(opts.FlagLevel); raw != "" {
		lvl, err := ParseLevel(raw)
		if err != nil {
			return 0, "", fmt.Errorf("--log-level: %w", err)
		}
		return lvl, SourceFlag, nil
	}
	if raw := strings.TrimSpace(opts.EnvLookup(EnvLevel)); raw != "" {
		lvl, err := ParseLevel(raw)
		if err != nil {
			return 0, "", fmt.Errorf("%s: %w", EnvLevel, err)
		}
		return lvl, SourceEnv, nil
	}
	return DefaultLevel, SourceDefault, nil
}

// ParseLevel decodes a case-insensitive level string. Accepts the four
// canonical names (debug / info / warn / error). Rejects everything
// else — including the legacy "warning" alias slog itself supports —
// so a typo is loud rather than silent.
func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q (want debug|info|warn|error)", raw)
	}
}

// Filename builds the per-day filename for one process. The component
// label is the second segment of `bacio-<component>-<date>.log`;
// callers that need a discriminator inside the component (e.g. a
// channel's session id or PID stamp) bake it into `component`.
//
// The date is in the process's local timezone — the brief explicitly
// says local-time rotation, which keeps the file name aligned with the
// timestamps inside.
func Filename(component string, t time.Time) string {
	return fmt.Sprintf("bacio-%s-%s.log", component, t.Format("2006-01-02"))
}

// NewHandlerOpts feeds NewHandler.
type NewHandlerOpts struct {
	// Dir is the resolved log directory. Created if missing.
	Dir string
	// Component is the slug embedded in the per-day filename and in
	// every log line under the "component" key.
	Component string
	// Level is the threshold below which events are silently dropped.
	Level slog.Level
	// Stderr is the secondary sink (set to io.Discard to silence it).
	// Defaults to os.Stderr when nil.
	Stderr io.Writer
	// Now lets tests inject a clock so rotation can be exercised
	// without sleeping a day.
	Now func() time.Time
}

// NewHandler returns an slog.Handler that fans events to both stderr
// and a per-day file under Dir. The directory is created on demand
// (mkdir -p); a creation failure is returned to the caller so it can
// fall back to a stderr-only handler.
//
// The returned handler is safe for concurrent use; an internal mutex
// serialises file rotation + writes.
func NewHandler(opts NewHandlerOpts) (*Handler, error) {
	if opts.Dir == "" {
		return nil, errors.New("logging: NewHandler called with empty Dir")
	}
	if opts.Component == "" {
		return nil, errors.New("logging: NewHandler called with empty Component")
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("logging: create log dir %s: %w", opts.Dir, err)
	}

	h := &Handler{
		opts:      opts,
		level:     opts.Level,
		stderrEnc: slog.NewTextHandler(opts.Stderr, &slog.HandlerOptions{Level: opts.Level}),
	}
	// Open the first file eagerly so a misconfigured permission
	// surfaces immediately instead of on the first log line. Errors
	// here propagate to the caller.
	if err := h.rotate(opts.Now()); err != nil {
		return nil, err
	}
	return h, nil
}

// StderrOnlyHandler returns a stderr-only handler — the graceful
// fallback callers use when NewHandler couldn't open a log file.
// Keeps every existing log line reachable without invasive plumbing.
func StderrOnlyHandler(level slog.Level) slog.Handler {
	return slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
}

// Handler is the slog.Handler that fans events to stderr + a per-day
// file. Implements slog.Handler.
type Handler struct {
	opts NewHandlerOpts

	mu        sync.Mutex
	file      *os.File
	fileEnc   slog.Handler
	dateStamp string // YYYY-MM-DD for the currently-open file

	level     slog.Level
	stderrEnc slog.Handler
}

// Enabled reports whether the event would be emitted at all. Used by
// slog before allocating an Event.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle writes the event to both sinks, rotating the file if the
// calendar day has rolled since the file was opened.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	stamp := h.opts.Now().Format("2006-01-02")
	if stamp != h.dateStamp {
		if err := h.rotate(h.opts.Now()); err != nil {
			// Rotation failed — fall through and write to stderr so
			// the operator still sees the line; the next call will
			// retry rotation.
			fmt.Fprintf(os.Stderr, "bacio logging: rotate failed: %v\n", err)
		}
	}
	// Stderr first so an operator tailing a process still sees the
	// line if the file write fails for any reason.
	_ = h.stderrEnc.Handle(ctx, r)
	if h.fileEnc != nil {
		_ = h.fileEnc.Handle(ctx, r)
	}
	return nil
}

// WithAttrs forwards to both sinks so With(...) clones produce
// attrs-carrying handlers that still fan out. The clone shares the
// parent's rotation lock + file handle (which can move under it on
// daily rotation), and re-applies the recorded attrs/groups onto the
// current file encoder on every Handle.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &attrClone{parent: h, ops: []attrOp{{attrs: attrs}}, stderrEnc: h.stderrEnc.WithAttrs(attrs)}
}

// WithGroup similarly forwards. Same lifetime/concurrency contract
// as WithAttrs.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &attrClone{parent: h, ops: []attrOp{{group: name}}, stderrEnc: h.stderrEnc.WithGroup(name)}
}

// attrOp records one WithAttrs/WithGroup step so the clone can replay
// the chain onto whichever file encoder the parent currently owns
// (the file encoder swaps on rotation; the clone has to re-derive
// each time).
type attrOp struct {
	attrs []slog.Attr
	group string // empty when this op is a WithAttrs
}

// attrClone is the cheap WithAttrs / WithGroup result. The clone's
// stderr encoder is fixed (slog's stderr handler doesn't rotate). The
// file encoder, by contrast, has to be re-derived from the parent's
// current file encoder on every Handle so a daily rotation doesn't
// leave the clone writing to a stale file handle.
type attrClone struct {
	parent    *Handler
	ops       []attrOp
	stderrEnc slog.Handler
}

func (a *attrClone) Enabled(ctx context.Context, level slog.Level) bool {
	return a.parent.Enabled(ctx, level)
}

func (a *attrClone) Handle(ctx context.Context, r slog.Record) error {
	a.parent.mu.Lock()
	defer a.parent.mu.Unlock()
	stamp := a.parent.opts.Now().Format("2006-01-02")
	if stamp != a.parent.dateStamp {
		if err := a.parent.rotate(a.parent.opts.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "bacio logging: rotate failed: %v\n", err)
		}
	}
	_ = a.stderrEnc.Handle(ctx, r)
	if a.parent.fileEnc != nil {
		fileEnc := a.parent.fileEnc
		for _, op := range a.ops {
			if op.group != "" {
				fileEnc = fileEnc.WithGroup(op.group)
			} else {
				fileEnc = fileEnc.WithAttrs(op.attrs)
			}
		}
		_ = fileEnc.Handle(ctx, r)
	}
	return nil
}

func (a *attrClone) WithAttrs(attrs []slog.Attr) slog.Handler {
	ops := append([]attrOp(nil), a.ops...)
	ops = append(ops, attrOp{attrs: attrs})
	return &attrClone{parent: a.parent, ops: ops, stderrEnc: a.stderrEnc.WithAttrs(attrs)}
}

func (a *attrClone) WithGroup(name string) slog.Handler {
	ops := append([]attrOp(nil), a.ops...)
	ops = append(ops, attrOp{group: name})
	return &attrClone{parent: a.parent, ops: ops, stderrEnc: a.stderrEnc.WithGroup(name)}
}

// rotate opens (or reopens) the per-day file. Caller holds h.mu.
func (h *Handler) rotate(now time.Time) error {
	stamp := now.Format("2006-01-02")
	if stamp == h.dateStamp && h.file != nil {
		return nil
	}
	if h.file != nil {
		_ = h.file.Close()
		h.file = nil
		h.fileEnc = nil
	}
	path := filepath.Join(h.opts.Dir, Filename(h.opts.Component, now))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("logging: open %s: %w", path, err)
	}
	h.file = f
	h.fileEnc = slog.NewTextHandler(f, &slog.HandlerOptions{Level: h.level})
	h.dateStamp = stamp
	return nil
}

// Close flushes any buffered state and closes the underlying file.
// Optional — bacio processes are long-lived and rely on OS cleanup —
// but tests use it to make assertions deterministic.
func (h *Handler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file == nil {
		return nil
	}
	err := h.file.Close()
	h.file = nil
	h.fileEnc = nil
	return err
}
