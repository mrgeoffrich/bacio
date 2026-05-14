package cli

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
)

var (
	cpuProfileFile *os.File
	traceFile      *os.File
)

// startProfiling opens the CPU profile and execution trace files (if the
// corresponding flags were given) and begins recording. It runs from
// PersistentPreRunE, before the command's RunE — for `bacio tui` that
// means recording is live before tea.NewProgram(...).Run() and covers
// the whole interactive session.
//
// Unlike CPU profiling, the execution trace captures off-CPU events
// (goroutine scheduling, blocking on syscalls/channels/mutexes), so it's
// the tool that actually diagnoses UI freezes — open it with
// `go tool trace <path>`.
func startProfiling() error {
	if opts.cpuProfile != "" {
		f, err := os.Create(opts.cpuProfile)
		if err != nil {
			return fmt.Errorf("create cpu profile: %w", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return fmt.Errorf("start cpu profile: %w", err)
		}
		cpuProfileFile = f
	}
	if opts.traceFile != "" {
		f, err := os.Create(opts.traceFile)
		if err != nil {
			return fmt.Errorf("create trace: %w", err)
		}
		if err := trace.Start(f); err != nil {
			f.Close()
			return fmt.Errorf("start trace: %w", err)
		}
		traceFile = f
	}
	return nil
}

// stopProfiling stops CPU profiling and writes the heap profile. main.go
// calls it unconditionally after Execute() returns so the profiles flush
// even when a command exits via an error path (cobra's PersistentPostRunE
// is skipped on error). Safe to call when profiling was never started.
func stopProfiling() {
	if cpuProfileFile != nil {
		pprof.StopCPUProfile()
		if err := cpuProfileFile.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "bacio: close cpu profile:", err)
		}
		cpuProfileFile = nil
	}
	if traceFile != nil {
		trace.Stop()
		if err := traceFile.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "bacio: close trace:", err)
		}
		traceFile = nil
	}
	if opts.memProfile == "" {
		return
	}
	f, err := os.Create(opts.memProfile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bacio: create mem profile:", err)
		return
	}
	defer f.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: write mem profile:", err)
	}
	opts.memProfile = ""
}
