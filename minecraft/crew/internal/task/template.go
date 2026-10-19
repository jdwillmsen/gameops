// Package task turns a small, closed set of JSON templates into skills and
// runs them one at a time. The templates are the only way to ask crew to act,
// so each is validated here before a task exists.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	gatherAndDeposit = "gather_and_deposit"
	minQty           = 1
	maxQty           = 256
	// How far from the unit MineBlock looks for stone.
	mineRadius = 24
)

type Chest struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
	Z int32 `json:"z"`
}

// Template is the v1 gather_and_deposit request.
type Template struct {
	Template string `json:"template"`
	Item     string `json:"item"`
	Qty      int    `json:"qty"`
	Chest    Chest  `json:"chest"`
}

// ParseTemplate decodes strictly: a misspelt or unused field is an error, not
// a silent default.
func ParseTemplate(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Item     string `json:"item"`
		Qty      int    `json:"qty"`
		Chest    *Chest `json:"chest"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Template{}, fmt.Errorf("invalid task: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Template{}, errors.New("invalid task: trailing data")
	}
	if raw.Template != gatherAndDeposit {
		return Template{}, errors.New("unknown template")
	}
	if _, ok := recipes[raw.Item]; !ok {
		return Template{}, errors.New("unknown item")
	}
	if raw.Qty < minQty || raw.Qty > maxQty {
		return Template{}, fmt.Errorf("qty must be %d to %d", minQty, maxQty)
	}
	if raw.Chest == nil {
		return Template{}, errors.New("chest is required")
	}
	return Template{Template: raw.Template, Item: raw.Item, Qty: raw.Qty, Chest: *raw.Chest}, nil
}

// Expand lists the skills a template stands for, in order.
func Expand(t Template, world skill.ChestWorld, self func() belief.Snapshot, opts ...skill.Option) ([]skill.Skill, error) {
	src, ok := recipes[t.Item]
	if t.Template != gatherAndDeposit || !ok {
		return nil, errors.New("unknown template or item")
	}
	chest := protocol.BlockPos{t.Chest.X, t.Chest.Y, t.Chest.Z}
	return []skill.Skill{
		skill.NewMineBlock(world, self, src.Block, t.Item, src.Tool, t.Qty, mineRadius),
		skill.NewDepositChest(world, self, chest, t.Item, t.Qty, opts...),
	}, nil
}
