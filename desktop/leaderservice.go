package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

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
	done    chan struct{}
	wg      sync.WaitGroup
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
	ls.st = s

	h, _ := os.Hostname()
	label := fmt.Sprintf("desktop pid=%d host=%s", os.Getpid(), h)
	ls.elector = leader.New(s, label)
	ls.done = make(chan struct{})

	ls.wg.Add(1)
	go func() {
		defer ls.wg.Done()
		// Tick immediately on startup so the frontend reflects the real
		// state on first load rather than waiting 10 s.
		ls.emitState(ls.elector.Tick())
		ticker := time.NewTicker(store.UILeaderHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ls.emitState(ls.elector.Tick())
			case <-ls.done:
				return
			}
		}
	}()

	// Live-list prune ticker: only the controlling UI prunes ended
	// agent_sessions older than AgentSessionLiveListRetention so two
	// side-by-side UIs don't race on the same DELETE. The ticker keeps
	// running regardless of leader state — the lease can flip back to us
	// mid-run — but the prune itself is gated on the cached state.
	// ServiceShutdown closes ls.done and ls.wg.Wait()s before the store
	// is closed, so an in-flight tick can never race with store close.
	ls.wg.Add(1)
	go func() {
		defer ls.wg.Done()
		ticker := time.NewTicker(store.UILeaderPruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !ls.elector.CurrentState().AmLeader {
					continue
				}
				if _, err := ls.st.PruneEndedAgentSessions(
					store.AgentSessionLiveListRetention); err != nil {
					fmt.Fprintln(os.Stderr,
						"bacio: live agent-session prune failed:", err)
				}
			case <-ls.done:
				return
			}
		}
	}()
	return nil
}

func (ls *LeaderService) ServiceShutdown() error {
	// Stop the ticker goroutine and wait for it to exit before releasing the
	// lease and closing the store — otherwise an in-flight Tick could run
	// against a closed store.
	if ls.done != nil {
		close(ls.done)
	}
	ls.wg.Wait()
	if ls.elector != nil {
		ls.elector.Release()
	}
	if ls.st != nil {
		ls.st.Close()
	}
	return nil
}

// GetLeaderStatus returns the current election state synchronously — used by
// the frontend on mount to seed its UI before the first event arrives.
func (ls *LeaderService) GetLeaderStatus() LeaderStatusDTO {
	if ls.elector == nil {
		return LeaderStatusDTO{}
	}
	s := ls.elector.CurrentState()
	return LeaderStatusDTO{AmLeader: s.AmLeader, HolderLabel: s.HolderLabel}
}

func (ls *LeaderService) emitState(s leader.State) {
	application.Get().Event.Emit("leaderStatus", LeaderStatusDTO{
		AmLeader:    s.AmLeader,
		HolderLabel: s.HolderLabel,
	})
}
