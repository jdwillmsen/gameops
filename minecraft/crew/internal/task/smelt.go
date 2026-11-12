package task

import (
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	smeltTemplate = "smelt"
	// A furnace takes one stack of an ingredient.
	maxSmeltQty = 64
)

// parseSmelt reads {"template":"smelt","item":"raw_iron","qty":8,"block":{x,y,z}}.
// block is the furnace's cell: a furnace standing there is used, and an empty
// cell gets the furnace the unit carries. The unit supplies its own fuel.
func parseSmelt(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Item     string `json:"item"`
		Qty      int    `json:"qty"`
		Block    *Chest `json:"block"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if _, ok := skill.SmeltOutput(raw.Item); !ok {
		return Template{}, errors.New("unknown item")
	}
	if raw.Qty < minQty || raw.Qty > maxSmeltQty {
		return Template{}, fmt.Errorf("qty must be %d to %d", minQty, maxSmeltQty)
	}
	if raw.Block == nil {
		return Template{}, errors.New("block is required")
	}
	// The message names no coordinate: they are lab locations and must stay
	// out of logs.
	if !raw.Block.inLab() {
		return Template{}, errors.New("block is outside the allowed area")
	}
	return Template{Template: raw.Template, Item: raw.Item, Qty: raw.Qty, Block: raw.Block}, nil
}

func expandSmelt(t Template, world skill.ChestWorld, self func() belief.Snapshot, opts []skill.Option) ([]skill.Skill, error) {
	sk, err := skill.NewSmelt(world, self, protocol.BlockPos{t.Block.X, t.Block.Y, t.Block.Z}, t.Item, t.Qty, opts...)
	if err != nil {
		return nil, err
	}
	return []skill.Skill{sk}, nil
}
