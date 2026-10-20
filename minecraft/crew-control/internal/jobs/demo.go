package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
)

type taskPos struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

type taskArea struct {
	Min taskPos `json:"min"`
	Max taskPos `json:"max"`
}

func pos(p ledger.Pos) taskPos { return taskPos{p.X, p.Y, p.Z} }

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// Only fixed, marshalable shapes are passed in.
		panic(err)
	}
	return b
}

func gatherTemplate(item string, qty int, area ledger.Box, chest ledger.Pos) json.RawMessage {
	return mustJSON(struct {
		Template string   `json:"template"`
		Item     string   `json:"item"`
		Qty      int      `json:"qty"`
		Chest    taskPos  `json:"chest"`
		Area     taskArea `json:"area"`
	}{"gather_and_deposit", item, qty, pos(chest), taskArea{pos(area.Min), pos(area.Max)}})
}

func haulTemplate(items map[string]int, from []ledger.Pos, to ledger.Pos) json.RawMessage {
	src := make([]taskPos, len(from))
	for i, f := range from {
		src[i] = pos(f)
	}
	return mustJSON(struct {
		Template string         `json:"template"`
		Items    map[string]int `json:"items"`
		From     []taskPos      `json:"from"`
		To       taskPos        `json:"to"`
	}{"haul", items, src, pos(to)})
}

func inspectTemplate(chest ledger.Pos) json.RawMessage {
	return mustJSON(struct {
		Template string  `json:"template"`
		Chest    taskPos `json:"chest"`
	}{"inspect", pos(chest)})
}

// ParallelDemo is the built-in plan: three units work at once and the hauler
// joins their results, which shows the scheduler's fan-out and fan-in.
func ParallelDemo(places ledger.Ledger) ([]Step, error) {
	ctx := context.Background()
	need := func(id string) (ledger.Place, error) {
		p, ok, err := places.Place(ctx, id)
		if err != nil {
			return ledger.Place{}, err
		}
		if !ok {
			return ledger.Place{}, fmt.Errorf("place %s not found", id)
		}
		return p, nil
	}
	var p struct{ quarry, grove, outA, outB, store ledger.Place }
	for _, f := range []struct {
		id  string
		dst *ledger.Place
	}{{"quarry-1", &p.quarry}, {"grove-1", &p.grove}, {"outbox-a", &p.outA}, {"outbox-b", &p.outB}, {"store", &p.store}} {
		got, err := need(f.id)
		if err != nil {
			return nil, err
		}
		*f.dst = got
	}
	return []Step{
		{Key: "mine", Unit: "crew-01", Template: gatherTemplate("cobblestone", 16, p.quarry.Box, p.outA.Box.Min)},
		{Key: "chop", Unit: "crew-02", Template: gatherTemplate("oak_log", 16, p.grove.Box, p.outB.Box.Min)},
		{Key: "haul", Unit: "crew-03", DependsOn: []string{"mine", "chop"},
			Template: haulTemplate(map[string]int{"cobblestone": 16, "oak_log": 16},
				[]ledger.Pos{p.outA.Box.Min, p.outB.Box.Min}, p.store.Box.Min)},
		{Key: "check", DependsOn: []string{"haul"}, Template: inspectTemplate(p.store.Box.Min)},
	}, nil
}
