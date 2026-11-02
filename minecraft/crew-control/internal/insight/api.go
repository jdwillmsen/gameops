package insight

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

// Gate is the part of auth.Auth the evidence view needs.
type Gate interface {
	Operator(r *http.Request) (auth.Operator, bool)
}

type API struct{ tracker *Tracker }

func NewAPI(t *Tracker) *API { return &API{tracker: t} }

type view struct {
	ID         int64      `json:"id"`
	Rule       string     `json:"rule"`
	Unit       string     `json:"unit,omitempty"`
	World      string     `json:"world,omitempty"`
	Fleet      bool       `json:"fleet,omitempty"`
	Severity   string     `json:"severity"`
	State      string     `json:"state"`
	FirstSeen  time.Time  `json:"first_seen"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	Detail     string     `json:"detail"`
	// Evidence is left out of anonymous answers: event ids only mean
	// something to the signed-in event explorer, and keep the public view
	// to the summary.
	Evidence []int64 `json:"evidence,omitempty"`
	// Attrs holds the numbers behind a finding. Detail never carries a count
	// or a percentage, so the numbers are signed-in only as well.
	Attrs map[string]any `json:"attrs,omitempty"`
}

// Mount registers GET /v1/insights. The summary is public, like unit status;
// the evidence event ids go to signed-in operators only.
func (a *API) Mount(mux *http.ServeMux, g Gate) {
	mux.HandleFunc("GET /v1/insights", func(w http.ResponseWriter, r *http.Request) {
		_, signed := g.Operator(r)
		list := a.tracker.List()
		out := make([]view, len(list))
		for i, in := range list {
			v := view{ID: in.ID, Rule: in.Rule, Unit: in.Unit, World: in.World, Fleet: in.Unit == "" && in.World == "", Severity: in.Severity,
				State: in.State, FirstSeen: in.FirstSeen, Detail: in.Detail}
			if !in.ResolvedAt.IsZero() {
				t := in.ResolvedAt
				v.ResolvedAt = &t
			}
			if signed {
				v.Evidence = in.Evidence
				v.Attrs = in.Attrs
			}
			out[i] = v
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			Insights []view `json:"insights"`
		}{out})
	})
}
