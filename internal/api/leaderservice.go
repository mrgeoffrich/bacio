package api

// UI leader election for `bacio api`. Mirrors desktop/leaderservice.go so a
// browser connected to the api can render the same "Controlling" chip when
// its server holds the lease. Both the desktop's LeaderService and this
// goroutine share the ui_leader table — exactly one process across all
// UIs (desktop, TUI, every running bacio api) holds the lease at a time.
//
// The api's elector runs alongside the http.Server: started inside
// Server.Run before ListenAndServe, stopped before Shutdown so the
// release lands before the listener closes. The three background
// loops — heartbeat, live-list prune, BACI-51 queue matcher — are
// driven by the shared internal/controller package so the api can't
// drift away from the desktop's tick set (which is how the matcher
// goroutine ended up missing here originally).

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mrgeoffrich/bacio/internal/controller"
	"github.com/mrgeoffrich/bacio/internal/dispatcher"
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

// apiLeaderService owns the elector + controller. The pointer lives on
// deps so GET /leader can read CurrentState() without contacting the store.
type apiLeaderService struct {
	elector *leader.Elector
	ctrl    *controller.Controller
}

// newAPILeaderService starts the elector and its tickers. The first heartbeat
// runs synchronously inside Controller.Start so GET /leader returns a non-zero
// state immediately after Run begins serving requests.
func newAPILeaderService(s *store.Store, addr string, logger *slog.Logger) *apiLeaderService {
	host, _ := os.Hostname()
	label := fmt.Sprintf("api pid=%d host=%s addr=%s", os.Getpid(), host, addr)
	el := leader.New(s, label)
	ls := &apiLeaderService{
		elector: el,
		ctrl:    controller.New(s, el, dispatcher.New(s), logger),
	}
	// emit=nil: the api has no event bus to push leader state through;
	// GET /leader reads it on demand.
	ls.ctrl.Start(nil)
	return ls
}

// stop closes the tick goroutines, waits for them to exit, and releases
// the lease.
func (ls *apiLeaderService) stop() {
	if ls.ctrl != nil {
		ls.ctrl.Stop()
	}
}

// currentState returns the elector's cached state — used by GET /leader.
func (ls *apiLeaderService) currentState() leader.State {
	return ls.elector.CurrentState()
}
