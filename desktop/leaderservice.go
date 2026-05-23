package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/leaderservice"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// LeaderStatusDTO is the state the desktop frontend receives on every
// election tick via the "leaderStatus" Wails event and via
// GetLeaderStatus on mount. It's a type alias of
// leaderservice.StatusDTO so the bacio api and desktop share one wire
// shape; the alias keeps the Wails binding generator emitting a type
// rooted at main.LeaderStatusDTO so the generated TS stays stable.
type LeaderStatusDTO = leaderservice.StatusDTO

// LeaderService runs the UI leader election for the desktop app. It
// satisfies the Wails v3 service lifecycle — ServiceStartup opens a
// dedicated *store.Store handle and starts the shared
// leaderservice.Service; ServiceShutdown stops it, releases the lease,
// then closes the store.
//
// It opens its own *store.Store (a second handle on the same WAL-mode
// file) rather than routing through client.Client, keeping it simple and
// consistent with how the CLI opens fresh store handles on every
// invocation.
type LeaderService struct {
	dbPath string // resolved at construction time so a per-worktree manifest (BACI-63) routes the leader at the same DB as the rest of the desktop
	slug   string // optional worktree slug — surfaces in the leader label so two desktop instances are distinguishable
	logger *slog.Logger
	st     *store.Store
	svc    *leaderservice.Service

	// mu guards svc reads from GetLeaderStatus during the early-startup
	// window before ServiceStartup has assigned it.
	mu sync.Mutex
}

// NewLeaderService creates an uninitialised LeaderService bound to a
// specific DB path and (optionally) a worktree slug. Wails calls
// ServiceStartup before the window opens. An empty dbPath falls back
// to store.DefaultPath() so old callers without a wtenv-resolved path
// still work.
//
// BACI-121: the desktop binary builds a file-backed *slog.Logger in
// main and threads it in here so leader-lease + background-sync +
// controller events land in the BACI-73 log file rather than relying
// on slog.Default(). A nil logger falls back to slog.Default() —
// preserves the historic behaviour for callers (e.g. tests) that
// haven't built one.
func NewLeaderService(dbPath, slug string, logger *slog.Logger) *LeaderService {
	return &LeaderService{dbPath: dbPath, slug: slug, logger: logger}
}

func (ls *LeaderService) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	dbPath := ls.dbPath
	if dbPath == "" {
		p, err := store.DefaultPath()
		if err != nil {
			return fmt.Errorf("leader election: resolve db path: %w", err)
		}
		dbPath = p
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("leader election: open store: %w", err)
	}

	h, _ := os.Hostname()
	label := fmt.Sprintf("desktop pid=%d host=%s", os.Getpid(), h)
	if ls.slug != "" {
		label = fmt.Sprintf("desktop[%s] pid=%d host=%s", ls.slug, os.Getpid(), h)
	}
	// BACI-121: pass an explicit logger so leader / sync / controller
	// events land in the file sink rather than relying on
	// slog.Default() (which is technically wired up too — main calls
	// slog.SetDefault — but the explicit hand-off is the contract).
	svc := leaderservice.New(s, label, dbPath, ls.logger)

	ls.mu.Lock()
	ls.st = s
	ls.svc = svc
	ls.mu.Unlock()

	// Start fires the heartbeat synchronously (so the frontend sees a
	// real state on first load rather than waiting 10s), then spins the
	// three leader-gated goroutines. emit pushes every subsequent
	// heartbeat through the Wails event bus.
	svc.Start(ls.emitState)
	return nil
}

func (ls *LeaderService) ServiceShutdown() error {
	// Stop the tick goroutines, wait for them to exit, and release the
	// lease before closing the store — otherwise an in-flight tick could
	// run against a closed store.
	if ls.svc != nil {
		ls.svc.Stop()
	}
	if ls.st != nil {
		ls.st.Close()
	}
	return nil
}

// GetLeaderStatus returns the current election state synchronously —
// used by the frontend on mount to seed its UI before the first event
// arrives.
func (ls *LeaderService) GetLeaderStatus() LeaderStatusDTO {
	ls.mu.Lock()
	svc := ls.svc
	ls.mu.Unlock()
	if svc == nil {
		return LeaderStatusDTO{}
	}
	s := svc.CurrentState()
	return LeaderStatusDTO{AmLeader: s.AmLeader, HolderLabel: s.HolderLabel}
}

func (ls *LeaderService) emitState(s leader.State) {
	application.Get().Event.Emit("leaderStatus", LeaderStatusDTO{
		AmLeader:    s.AmLeader,
		HolderLabel: s.HolderLabel,
	})
}
