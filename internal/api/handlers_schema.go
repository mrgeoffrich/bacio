package api

import (
	"encoding/json"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/schema"
)

type schemaListRow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (d deps) handleSchemaAll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(schema.All())
}

func (d deps) handleSchemaList(w http.ResponseWriter, r *http.Request) {
	out := make([]schemaListRow, 0, len(schema.Registry))
	for _, e := range schema.Registry {
		out = append(out, schemaListRow{Name: e.Name, Description: e.Short})
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func (d deps) handleSchemaShow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entry, ok := schema.Find(name)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown schema "+name, nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(schema.Build(entry))
}
