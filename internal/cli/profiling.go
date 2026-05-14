package cli

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
)

var cpuProfileFile *os.File

// startProfiling opens the CPU profile file (if --cpuprofile was given)
// and begins CPU profiling. It runs from PersistentPreRunE, before the
// command's RunE — for `bacio tui` that means profiling is live before
// tea.NewProgram(...).Run() and covers the whole interactive session.
func startProfiling() error {
	if opts.cpuProfile == "" {
		return nil
	}
	f, err := os.Create(opts.cpuProfile)
	if err != nil {
		return fmt.Errorf("create cpu profile: %w", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return fmt.Errorf("start cpu profile: %w", err)
	}
	cpuProfileFile = f
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
