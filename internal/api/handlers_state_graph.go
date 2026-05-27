package api

import (
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/stategraph"
)

// stateGraphOut is the wire shape of GET /settings/state-graph (BACI-241).
//
// `states` is the canonical state ordering (from model.AllStates), so a
// client can render a state-picker without a second round trip. `edges`
// is the categorised edge table — each (from, to, category) triple is
// one row of the graph. The categorisation is a display hint, not an
// enforcement layer; the store still accepts any state-to-state move.
type stateGraphOut struct {
	States []model.State    `json:"states"`
	Edges  []stategraph.Edge `json:"edges"`
}

// handleStateGraph returns the BACI-241 canonical state-transition
// graph. Thin marshaller — the data is a Go constant, no per-request
// cost. Same shape on every call; no parameters.
func (d deps) handleStateGraph(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, stateGraphOut{
		States: model.AllStates(),
		Edges:  stategraph.Edges(),
	})
}
