package api

// UI leader election for `bacio api`. Mirrors desktop/leaderservice.go so a
// browser connected to the api can render the same "Controlling" chip when
// its server holds the lease. Both the desktop's LeaderService and this
// goroutine share the ui_leader table — exactly one process across all
// UIs (desktop, TUI, every running bacio api) holds the lease at a time.
//
// The api's elector runs alongside the http.Server: started inside
// Server.Run before ListenAndServe, stopped before Shutdown so the
// release lands before the listener closes. A second goroutine prunes
// ended agent_sessions on the 5-minute cadence, gated on the cached
// leader state — same pattern as desktop/leaderservice.go.

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// LeaderStatusDTO is the response shape for GET /leader. Mirrors the
// desktop's LeaderStatusDTO so the same frontend type (LeaderStatusDTO
// in api.http.ts) works against either backend.
type LeaderStatusDTO struct {
	AmLeader    bool   `json:"amLeader"`
	HolderLabel string `json:"holderLabel"`
}

// apiLeaderService owns the elector + tick goroutines. The pointer lives on
// deps so GET /leader can read CurrentState() without contacting the store.
type apiLeaderService struct {
	elector *leader.Elector
	done    chan struct{}
	wg      sync.WaitGroup
}

// newAPILeaderService starts the elector and its tickers. The first tick
// runs synchronously so GET /leader returns a non-zero state immediately
// after Run begins serving requests.
func newAPILeaderService(s *store.Store, addr string, logger *slog.Logger) *apiLeaderService {
	host, _ := os.Hostname()
	label := fmt.Sprintf("api pid=%d host=%s addr=%s", os.Getpid(), host, addr)
	ls := &apiLeaderService{
		elector: leader.New(s, label),
		done:    make(chan struct{}),
	}
	ls.elector.Tick()

	ls.wg.Add(1)
	go func() {
		defer ls.wg.Done()
		ticker := time.NewTicker(store.UILeaderHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ls.elector.Tick()
			case <-ls.done:
				return
			}
		}
	}()

	// Live-list prune ticker — only the controlling UI prunes ended
	// agent_sessions older than AgentSessionLiveListRetention, so two
	// side-by-side UIs don't race on the same DELETE. Mirrors the
	// desktop's identical loop in leaderservice.go.
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
				if _, err := s.PruneEndedAgentSessions(
					store.AgentSessionLiveListRetention); err != nil {
					logger.Warn("api: live agent-session prune failed", "err", err)
				}
			case <-ls.done:
				return
			}
		}
	}()

	return ls
}

// stop closes the tick goroutines, waits for them to exit, and releases
// the lease. Safe to call multiple times — close() will panic the second
// time on a re-close, but Server.Run only calls this once.
func (ls *apiLeaderService) stop() {
	close(ls.done)
	ls.wg.Wait()
	ls.elector.Release()
}

// currentState returns the elector's cached state — used by GET /leader.
func (ls *apiLeaderService) currentState() leader.State {
	return ls.elector.CurrentState()
}
