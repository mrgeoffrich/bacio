package api

// UI leader election for `bacio api`. Mirrors desktop/leaderservice.go
// so a browser connected to the api can render the same "Controlling"
// chip when its server holds the lease. Both the desktop's
// LeaderService and this shim share the ui_leader table — exactly one
// process across all UIs (desktop, TUI, every running bacio api) holds
// the lease at a time.
//
// The api's elector runs alongside the http.Server: started inside
// Server.Run before ListenAndServe, stopped before Shutdown so the
// release lands before the listener closes. The three background loops
// — heartbeat, live-list prune, BACI-51 queue matcher — are driven by
// the shared internal/leaderservice package (which composes
// internal/controller's tick set) so the api can't drift away from the
// desktop's loop set.

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/leaderservice"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// LeaderStatusDTO is the response shape for GET /leader. Type alias of
// leaderservice.StatusDTO so the bacio api and the desktop's
// "leaderStatus" Wails event share one wire format; the alias keeps
// api.LeaderStatusDTO addressable from existing callers (docs,
// handlers, tests).
type LeaderStatusDTO = leaderservice.StatusDTO

// apiLeaderService owns the elector + controller via the shared
// leaderservice.Service. The pointer lives on deps so GET /leader can
// read CurrentState() without contacting the store.
type apiLeaderService struct {
	svc *leaderservice.Service
}

// newAPILeaderService starts the leaderservice. The first heartbeat
// runs synchronously inside Service.Start so GET /leader returns a
// non-zero state immediately after Run begins serving requests. dbPath
// is threaded to the BACI-89 background sync runner's cross-process
// lock.
func newAPILeaderService(s *store.Store, addr, dbPath string, logger *slog.Logger) *apiLeaderService {
	host, _ := os.Hostname()
	label := fmt.Sprintf("api pid=%d host=%s addr=%s", os.Getpid(), host, addr)
	ls := &apiLeaderService{svc: leaderservice.New(s, label, dbPath, logger)}
	// emit=nil: the api has no event bus to push leader state through;
	// GET /leader reads it on demand.
	ls.svc.Start(nil)
	return ls
}

// stop closes the tick goroutines, waits for them to exit, and releases
// the lease.
func (ls *apiLeaderService) stop() {
	if ls.svc != nil {
		ls.svc.Stop()
	}
}

// currentState returns the elector's cached state — used by GET /leader.
func (ls *apiLeaderService) currentState() leader.State {
	return ls.svc.CurrentState()
}

// syncInProgress reports whether this process's background sync runner
// is mid-tick (BACI-89). Only meaningful when this process is the
// leader; a standby always reports false.
func (ls *apiLeaderService) syncInProgress() bool {
	if ls == nil || ls.svc == nil {
		return false
	}
	return ls.svc.SyncInProgress()
}
