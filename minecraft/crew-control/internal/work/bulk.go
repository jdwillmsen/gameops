package work

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

// maxBulk bounds one request's blast radius and its store work: each unit
// costs up to two writes, and a fleet is far smaller than this.
const maxBulk = 16

// bulkResult is one unit's outcome. A refusal for one unit never undoes the
// others, so the caller reads this list, not the status code.
type bulkResult struct {
	Unit  string     `json:"unit"`
	OK    bool       `json:"ok"`
	ID    *uuid.UUID `json:"id,omitempty"`
	Error string     `json:"error,omitempty"`
}

// bulk recalls (stops, then walks to a point) or assigns (queues one task
// to) several units. Everything that can be checked without the store is
// checked first, so a bad request queues nothing. confirm must equal the
// number of units: a client that did not show the operator the count cannot
// send it.
func (a *API) bulk(w http.ResponseWriter, r *http.Request, op string) {
	var req struct {
		Units   []string        `json:"units"`
		Confirm *int            `json:"confirm"`
		Action  string          `json:"action"`
		To      *point          `json:"to"`
		Task    json.RawMessage `json:"task"`
	}
	if err := decodeStrict(http.MaxBytesReader(w, r.Body, maxTaskBody), &req); err != nil {
		opError(w, http.StatusBadRequest, "bad bulk request")
		return
	}
	if n := len(req.Units); n < 1 || n > maxBulk {
		opError(w, http.StatusBadRequest, "units must list 1 to 16 units")
		return
	}
	if req.Confirm == nil || *req.Confirm != len(req.Units) {
		opError(w, http.StatusBadRequest, "confirm must equal the number of units")
		return
	}
	seen := make(map[string]bool, len(req.Units))
	for _, u := range req.Units {
		if seen[u] {
			opError(w, http.StatusBadRequest, "a unit is listed twice")
			return
		}
		seen[u] = true
		if _, ok := a.units.Get(u); !ok {
			opError(w, http.StatusNotFound, "no such unit")
			return
		}
	}
	var task []byte
	stopFirst := false
	switch req.Action {
	case "recall":
		if err := req.To.check(); err != nil {
			opError(w, http.StatusBadRequest, err.Error())
			return
		}
		task, _ = json.Marshal(map[string]any{"template": "goto", "to": req.To})
		stopFirst = true
	case "assign":
		task = req.Task
		if err := validateTemplate(task); err != nil {
			opError(w, http.StatusBadRequest, err.Error())
			return
		}
	default:
		opError(w, http.StatusBadRequest, "action must be recall or assign")
		return
	}

	results := make([]bulkResult, 0, len(req.Units))
	for _, u := range req.Units {
		res := bulkResult{Unit: u}
		fail := func(err error) {
			res.Error = storeMessage(err)
		}
		if stopFirst {
			if _, _, _, _, err := a.store.StopUnit(r.Context(), u, op, nil); err != nil {
				fail(err)
				results = append(results, res)
				continue
			}
		}
		it, err := a.store.Enqueue(r.Context(), u, KindTask, task, op)
		a.changed(u)
		if err != nil {
			fail(err)
		} else {
			res.OK, res.ID = true, &it.ID
		}
		results = append(results, res)
	}
	opJSON(w, http.StatusOK, map[string]any{"results": results})
}
