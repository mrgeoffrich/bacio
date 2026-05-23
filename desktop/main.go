package main

import (
	"context"
	"embed"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/logging"
	"github.com/mrgeoffrich/bacio/internal/version"
	"github.com/mrgeoffrich/bacio/internal/wtenv"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Register a custom event whose associated data type is string.
	// This is not required, but the binding generator will pick up registered events
	// and provide a strongly typed JS/TS API for them.
	application.RegisterEvent[string]("time")
	// leaderStatus fires on every UI leader-election tick (~10s). The frontend
	// subscribes to it to keep the "Controlling / Standby" chip up to date.
	application.RegisterEvent[LeaderStatusDTO]("leaderStatus")
}

// main function serves as the application's entry point. It initializes the application, creates a window,
// and starts a goroutine that emits a time-based event every second. It subsequently runs the application and
// logs any error that might occur.
// desktopFlags carries the small CLI surface the desktop binary
// accepts. All fields override the worktree manifest resolution
// chain (BACI-63 for DB/env, BACI-73 for log dir/level); empty
// values let the resolver pick.
type desktopFlags struct {
	DBPath   string
	EnvPath  string
	LogDir   string
	LogLevel string
}

func parseDesktopFlags(args []string) desktopFlags {
	fs := flag.NewFlagSet("bacio-desktop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var df desktopFlags
	fs.StringVar(&df.DBPath, "db", "", "override database path (resolves before the worktree manifest chain)")
	fs.StringVar(&df.EnvPath, "env", "", "path to a worktree environment manifest (overrides $BACIO_ENV)")
	fs.StringVar(&df.LogDir, "log-dir", "", "directory for file logs (resolves: flag > $BACIO_LOG_DIR > worktree manifest > ~/.bacio/logs/)")
	fs.StringVar(&df.LogLevel, "log-level", "", "log level: debug|info|warn|error (default info; falls back to $BACIO_LOG_LEVEL)")
	_ = fs.Parse(args)
	return df
}

// buildDesktopLogger wires the BACI-73 file-logging handler for the
// desktop binary. The component label is "desktop" so log files land
// at bacio-desktop-YYYY-MM-DD.log. Resolution mirrors the CLI's chain:
// flag > env > worktree manifest > default fallback. Failures fall
// back to a stderr-only handler so a bad knob never blocks the GUI.
func buildDesktopLogger(env wtenv.Resolved, df desktopFlags) (*slog.Logger, func()) {
	opts := logging.ResolveOpts{
		FlagDir:   df.LogDir,
		FlagLevel: df.LogLevel,
	}
	if env.ManifestPath != "" {
		opts.ManifestDir = filepath.Dir(env.ManifestPath)
		if env.Manifest != nil {
			opts.ManifestLogDir = env.Manifest.Allocations.LogDir
		}
	}
	logRes, err := logging.Resolve(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bacio-desktop: log config: %v — file logging disabled\n", err)
		return slog.New(logging.StderrOnlyHandler(slog.LevelInfo)).With("component", "desktop"), func() {}
	}
	h, hErr := logging.NewHandler(logging.NewHandlerOpts{
		Dir:       logRes.Dir,
		Component: "desktop",
		Level:     logRes.Level,
	})
	if hErr != nil {
		fmt.Fprintf(os.Stderr, "bacio-desktop: file logging disabled: %v\n", hErr)
		return slog.New(logging.StderrOnlyHandler(logRes.Level)).With("component", "desktop"), func() {}
	}
	return slog.New(h).With("component", "desktop"), func() { _ = h.Close() }
}

// resolveDesktopEnv runs wtenv.Resolve against the captured cwd and
// the supplied flag values, so the desktop binary picks up the same
// per-worktree DB the CLI / api / channel do.
func resolveDesktopEnv(cwd string, df desktopFlags) (wtenv.Resolved, error) {
	envLookup := os.Getenv
	if df.EnvPath != "" {
		envLookup = func(k string) string {
			if k == wtenv.EnvVar {
				return df.EnvPath
			}
			return os.Getenv(k)
		}
	}
	return wtenv.Resolve(wtenv.ResolveOpts{
		Cwd:       cwd,
		FlagDB:    df.DBPath,
		EnvLookup: envLookup,
	})
}

func main() {
	// Capture cwd before Wails has a chance to chdir — wtenv.Resolve's
	// worktree-root walk needs it.
	cwd, _ := os.Getwd()
	df := parseDesktopFlags(os.Args[1:])
	resolved, err := resolveDesktopEnv(cwd, df)
	if err != nil {
		log.Fatalf("resolve env: %v", err)
	}
	if resolved.ManifestPath != "" {
		fmt.Fprintf(os.Stderr, "bacio-desktop: env source=%s db=%s manifest=%s\n", resolved.Source, resolved.DBPath, resolved.ManifestPath)
	}

	// BACI-73: wire the file-logging handler. Falls back to
	// stderr-only on any resolution / open error so a misconfigured
	// log knob can never prevent the desktop from starting.
	logger, closeLogger := buildDesktopLogger(resolved, df)
	defer closeLogger()
	// Make slog.Default() route through the file sink so every
	// service that uses slog without explicit plumbing (LeaderService,
	// BoardService, etc.) inherits the same handler.
	slog.SetDefault(logger)
	logger.Info("bacio-desktop starting",
		"db", resolved.DBPath,
		"env_source", string(resolved.Source),
		"env_path", resolved.ManifestPath,
		"manifest_slug", resolved.ManifestSlug(),
		"version", version.String(),
	)

	// Open a local bacio client — the same API surface the CLI uses.
	// DBPath defaults to the resolved worktree manifest's value (or
	// the legacy ~/.bacio/db.sqlite when no manifest is in play).
	c, err := client.Open(context.Background(), client.Options{
		Actor:  "desktop",
		DBPath: resolved.DBPath,
	})
	if err != nil {
		log.Fatalf("open bacio client: %v", err)
	}
	defer c.Close()

	// Create a new Wails application by providing the necessary options.
	// Variables 'Name' and 'Description' are for application metadata.
	// 'Assets' configures the asset server with the 'FS' variable pointing to the frontend files.
	// 'Bind' is a list of Go struct instances. The frontend has access to the methods of these instances.
	// 'Mac' options tailor the application when running an macOS.
	app := application.New(application.Options{
		Name:        "bacio-desktop",
		Description: "A demo of using raw HTML & CSS",
		Services: []application.Service{
			application.NewService(NewBoardService(c)),
			application.NewService(NewDocService(c)),
			application.NewService(NewFeatureService(c)),
			application.NewService(NewHistoryService(c)),
			application.NewService(NewSettingsService(c)),
			application.NewService(NewLeaderService(resolved.DBPath, resolved.ManifestSlug(), logger)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Create a new window with the necessary options.
	// 'Title' is the title of the window.
	// 'Mac' options tailor the window when running on macOS.
	// 'BackgroundColour' is the background colour of the window.
	// 'URL' is the URL that will be loaded into the webview.
	windowTitle := "bacio"
	if slug := resolved.ManifestSlug(); slug != "" {
		// Two desktop windows from sibling worktrees are visually
		// identical otherwise — putting the manifest slug in the title
		// is the cheapest way to tell them apart on the taskbar.
		windowTitle = "bacio [" + slug + "]"
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: windowTitle,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		// Neutral mid-slate: the window background is briefly visible on a
		// cold start before the webview paints. It can't match the resolved
		// theme — Wails only wires up OS-appearance detection once Run()
		// starts, after this window is already created — so a mid-tone keeps
		// the flash mild in both light and dark mode rather than flashing
		// bright white in dark mode.
		BackgroundColour: application.NewRGB(131, 137, 149),
		URL:              "/",
	})

	// Create a goroutine that emits an event containing the current time every second.
	// The frontend can listen to this event and update the UI accordingly.
	go func() {
		for {
			now := time.Now().Format(time.RFC1123)
			app.Event.Emit("time", now)
			time.Sleep(time.Second)
		}
	}()

	// Translate SIGINT/SIGTERM/SIGHUP into a graceful app.Quit so Wails
	// runs ServiceShutdown on LeaderService — which releases the UI
	// leader lease so a standby UI promotes within one tick (~10s)
	// rather than waiting out the 180s stale window.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		if _, ok := <-sigCh; ok {
			app.Quit()
		}
	}()
	defer signal.Stop(sigCh)

	// Run the application. This blocks until the application has been exited.
	err = app.Run()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}
