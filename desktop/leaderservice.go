package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/mrgeoffrich/bacio/internal/controller"
	"github.com/mrgeoffrich/bacio/internal/dispatcher"
	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// LeaderStatusDTO is the state the desktop frontend receives on every election
// tick via the "leaderStatus" Wails event and via GetLeaderStatus on mount.
type LeaderStatusDTO struct {
	// AmLeader is true when this desktop process holds the UI leader lease.
	AmLeader bool `json:"amLeader"`
	// HolderLabel is the human-readable label of the process that currently
	// holds the lease — useful for "Standby — {HolderLabel} has control".
	// Empty when AmLeader is true or the lease has never been acquired.
	HolderLabel string `json:"holderLabel"`
}

// LeaderService runs the UI leader election for the desktop app. It satisfies
// the Wails v3 service lifecycle — ServiceStartup starts the election loop,
// ServiceShutdown stops it and releases the lease gracefully.
//
// It opens its own *store.Store (a second handle on the same WAL-mode file)
// rather than routing through client.Client, keeping it simple and consistent
// with how the CLI opens fresh store handles on every invocation.
type LeaderService struct {
	st      *store.Store
	elector *leader.Elector
	ctrl    *controller.Controller

	// mu guards elector reads from GetLeaderStatus during the early-startup
	// window before ServiceStartup has assigned it.
	mu sync.Mutex
}

// NewLeaderService creates an uninitialised LeaderService; Wails calls
// ServiceStartup before the window opens.
func NewLeaderService() *LeaderService {
	return &LeaderService{}
}

func (ls *LeaderService) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	dbPath, err := store.DefaultPath()
	if err != nil {
		return fmt.Errorf("leader election: resolve db path: %w", err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("leader election: open store: %w", err)
	}

	h, _ := os.Hostname()
	label := fmt.Sprintf("desktop pid=%d host=%s", os.Getpid(), h)
	el := leader.New(s, label)

	ls.mu.Lock()
	ls.st = s
	ls.elector = el
	ls.ctrl = controller.New(s, el, dispatcher.New(s), nil)
	ls.mu.Unlock()

	// Start fires the heartbeat synchronously (so the frontend sees a real
	// state on first load rather than waiting 10 s), then spins the three
	// leader-gated goroutines. emit pushes every subsequent heartbeat
	// through the Wails event bus.
	ls.ctrl.Start(ls.emitState)
	return nil
}

func (ls *LeaderService) ServiceShutdown() error {
	// Stop the tick goroutines, wait for them to exit, and release the
	// lease before closing the store — otherwise an in-flight tick could
	// run against a closed store.
	if ls.ctrl != nil {
		ls.ctrl.Stop()
	}
	if ls.st != nil {
		ls.st.Close()
	}
	return nil
}

// GetLeaderStatus returns the current election state synchronously — used by
// the frontend on mount to seed its UI before the first event arrives.
func (ls *LeaderService) GetLeaderStatus() LeaderStatusDTO {
	ls.mu.Lock()
	el := ls.elector
	ls.mu.Unlock()
	if el == nil {
		return LeaderStatusDTO{}
	}
	s := el.CurrentState()
	return LeaderStatusDTO{AmLeader: s.AmLeader, HolderLabel: s.HolderLabel}
}

func (ls *LeaderService) emitState(s leader.State) {
	application.Get().Event.Emit("leaderStatus", LeaderStatusDTO{
		AmLeader:    s.AmLeader,
		HolderLabel: s.HolderLabel,
	})
}
