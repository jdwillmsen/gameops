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
	haulTemplate     = "haul"
	maxHaulItems     = 8
	maxHaulSources   = 4
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

// Template is a task request: gather_and_deposit fills Item, Qty and Chest;
// haul fills Items, From and To.
type Template struct {
	Template string `json:"template"`
	Item     string `json:"item"`
	Qty      int    `json:"qty"`
	Chest    Chest  `json:"chest"`

	Items map[string]int `json:"items,omitempty"`
	From  []Chest        `json:"from,omitempty"`
	To    *Chest         `json:"to,omitempty"`
}

// decodeStrict rejects unknown fields and anything after the object.
func decodeStrict(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid task: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("invalid task: trailing data")
	}
	return nil
}

// ParseTemplate decodes strictly: a misspelt or unused field is an error, not
// a silent default.
func ParseTemplate(body []byte) (Template, error) {
	var head struct {
		Template string `json:"template"`
	}
	// Which fields are allowed depends on the template, so the name is read
	// first without the strictness the full decode applies.
	if err := json.Unmarshal(body, &head); err != nil {
		return Template{}, fmt.Errorf("invalid task: %w", err)
	}
	switch head.Template {
	case gatherAndDeposit:
		return parseGather(body)
	case haulTemplate:
		return parseHaul(body)
	}
	if err := decodeStrict(body, &struct {
		Template string `json:"template"`
		Item     string `json:"item"`
		Qty      int    `json:"qty"`
		Chest    *Chest `json:"chest"`
	}{}); err != nil {
		return Template{}, err
	}
	return Template{}, errors.New("unknown template")
}

func parseGather(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Item     string `json:"item"`
		Qty      int    `json:"qty"`
		Chest    *Chest `json:"chest"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
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

func parseHaul(body []byte) (Template, error) {
	var raw struct {
		Template string         `json:"template"`
		Items    map[string]int `json:"items"`
		From     []Chest        `json:"from"`
		To       *Chest         `json:"to"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if len(raw.Items) < 1 || len(raw.Items) > maxHaulItems {
		return Template{}, fmt.Errorf("items must list %d to %d items", 1, maxHaulItems)
	}
	total := 0
	for item, qty := range raw.Items {
		total += qty
		if _, ok := recipes[item]; !ok {
			return Template{}, errors.New("unknown item")
		}
		if qty < minQty || qty > maxQty {
			return Template{}, fmt.Errorf("qty must be %d to %d", minQty, maxQty)
		}
	}
	if len(raw.From) < 1 || len(raw.From) > maxHaulSources {
		return Template{}, fmt.Errorf("from must list %d to %d chests", 1, maxHaulSources)
	}
	if raw.To == nil {
		return Template{}, errors.New("to is required")
	}
	return Template{Template: raw.Template, Qty: total, Items: raw.Items, From: raw.From, To: raw.To}, nil
}

// Expand lists the skills a template stands for, in order.
func Expand(t Template, world skill.ChestWorld, self func() belief.Snapshot, opts ...skill.Option) ([]skill.Skill, error) {
	if t.Template == haulTemplate && t.To != nil {
		sources := make([]protocol.BlockPos, len(t.From))
		for i, c := range t.From {
			sources[i] = protocol.BlockPos{c.X, c.Y, c.Z}
		}
		dest := protocol.BlockPos{t.To.X, t.To.Y, t.To.Z}
		return []skill.Skill{skill.NewHaul(world, self, sources, dest, t.Items, opts...)}, nil
	}
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
