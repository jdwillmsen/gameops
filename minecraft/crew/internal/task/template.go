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
	"regexp"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	gatherAndDeposit = "gather_and_deposit"
	haulTemplate     = "haul"
	shaftTemplate    = "mine_shaft"
	maxShaftDepth    = 32
	maxBranches      = 3
	maxBranchLength  = 32
	gotoTemplate     = "goto"
	inspectTemplate  = "inspect"
	observeTemplate  = "observe_blocks"
	placeTemplate    = "place_block"
	// maxObserveCells caps a box at what one report can carry.
	maxObserveCells = 4096
	maxHaulItems    = 8
	maxHaulSources  = 4
	minQty          = 1
	maxQty          = 256
	maxAreaSpan     = 64
	// How far from the unit MineBlock looks for stone.
	mineRadius = 24
	// goto is confined to the lab's flat square, so a typo cannot send the
	// unit off to unloaded terrain.
	gotoMinXZ, gotoMaxXZ = -500, 499
	gotoMinY, gotoMaxY   = -64, 320
	// gotoWithin is how close to the cell counts as arrived.
	gotoWithin = 1.5
)

// validItemName is the shape of an item place_block may be asked for, the
// server's item-table names: lower case, digits and underscores.
var validItemName = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

type Chest struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
	Z int32 `json:"z"`
}

// Template is a task request: gather_and_deposit fills Item, Qty and Chest;
// haul fills Items, From and To; place_block fills Block and Item.
type Template struct {
	Template string `json:"template"`
	Item     string `json:"item"`
	Qty      int    `json:"qty"`
	Chest    Chest  `json:"chest"`
	// Block, when set, is the cell place_block fills.
	Block *Chest `json:"block,omitempty"`
	// Area, when set, confines gather_and_deposit to blocks inside the box.
	Area *Area `json:"area,omitempty"`
	// Box, when set, is what observe_blocks reads.
	Box *Box `json:"box,omitempty"`

	Items map[string]int `json:"items,omitempty"`
	From  []Chest        `json:"from,omitempty"`
	To    *Chest         `json:"to,omitempty"`

	Entrance     *Chest `json:"entrance,omitempty"`
	Facing       string `json:"facing,omitempty"`
	Depth        int    `json:"depth,omitempty"`
	Branches     int    `json:"branches,omitempty"`
	BranchLength int    `json:"branch_length,omitempty"`
}

// facings maps a heading to its step in x and z: north is toward negative z.
var facings = map[string][2]int32{"north": {0, -1}, "south": {0, 1}, "east": {1, 0}, "west": {-1, 0}}

// Area is an inclusive box, min at most max on every axis.
type Area struct {
	Min Chest `json:"min"`
	Max Chest `json:"max"`
}

// Box is an inclusive box to observe, min at most max on every axis. It is
// not Area: an observation may span more than Area's 64 blocks on an axis.
type Box struct {
	Min Chest `json:"min"`
	Max Chest `json:"max"`
}

// validate accepts only a box whose observation can travel in one report.
func (b Box) validate() error {
	// int64 so a span across the int32 range cannot wrap.
	cells := int64(1)
	for _, ax := range [][2]int32{{b.Min.X, b.Max.X}, {b.Min.Y, b.Max.Y}, {b.Min.Z, b.Max.Z}} {
		if ax[0] > ax[1] {
			return errors.New("box min must not exceed max")
		}
		cells *= int64(ax[1]) - int64(ax[0]) + 1
	}
	if cells > maxObserveCells {
		return fmt.Errorf("box must hold at most %d cells", maxObserveCells)
	}
	return nil
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
	case shaftTemplate:
		return parseShaft(body)
	case gotoTemplate:
		return parseGoto(body)
	case inspectTemplate:
		return parseInspect(body)
	case observeTemplate:
		return parseObserve(body)
	case placeTemplate:
		return parsePlace(body)
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
		Area     *struct {
			Min *Chest `json:"min"`
			Max *Chest `json:"max"`
		} `json:"area"`
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
	t := Template{Template: raw.Template, Item: raw.Item, Qty: raw.Qty, Chest: *raw.Chest}
	if raw.Area != nil {
		if raw.Area.Min == nil || raw.Area.Max == nil {
			return Template{}, errors.New("area needs min and max")
		}
		a := Area{Min: *raw.Area.Min, Max: *raw.Area.Max}
		if err := a.validate(); err != nil {
			return Template{}, err
		}
		t.Area = &a
	}
	return t, nil
}

func (a Area) validate() error {
	// int64 so a span across the int32 range cannot wrap.
	for _, ax := range [][2]int32{{a.Min.X, a.Max.X}, {a.Min.Y, a.Max.Y}, {a.Min.Z, a.Max.Z}} {
		if ax[0] > ax[1] {
			return errors.New("area min must not exceed max")
		}
		if int64(ax[1])-int64(ax[0])+1 > maxAreaSpan {
			return fmt.Errorf("area must span at most %d blocks on each axis", maxAreaSpan)
		}
	}
	return nil
}

func parseShaft(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Entrance *Chest `json:"entrance"`
		Facing   string `json:"facing"`
		Depth    int    `json:"depth"`
		Branches int    `json:"branches"`
		Length   int    `json:"branch_length"`
		Area     *struct {
			Min *Chest `json:"min"`
			Max *Chest `json:"max"`
		} `json:"area"`
		To *Chest `json:"to"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Entrance == nil {
		return Template{}, errors.New("entrance is required")
	}
	if _, ok := facings[raw.Facing]; !ok {
		return Template{}, errors.New("facing must be north, south, east or west")
	}
	if raw.Depth < 1 || raw.Depth > maxShaftDepth {
		return Template{}, fmt.Errorf("depth must be 1 to %d", maxShaftDepth)
	}
	if raw.Branches < 0 || raw.Branches > maxBranches {
		return Template{}, fmt.Errorf("branches must be 0 to %d", maxBranches)
	}
	if raw.Length < 0 || raw.Length > maxBranchLength || (raw.Branches > 0) != (raw.Length > 0) {
		return Template{}, fmt.Errorf("branch_length must be 1 to %d with branches, and absent without", maxBranchLength)
	}
	// The lab bound is not optional for a template that removes terrain.
	if raw.Area == nil || raw.Area.Min == nil || raw.Area.Max == nil {
		return Template{}, errors.New("area with min and max is required")
	}
	a := Area{Min: *raw.Area.Min, Max: *raw.Area.Max}
	if err := a.validate(); err != nil {
		return Template{}, err
	}
	if raw.To == nil {
		return Template{}, errors.New("to is required")
	}
	return Template{Template: raw.Template, Entrance: raw.Entrance, Facing: raw.Facing, Depth: raw.Depth,
		Branches: raw.Branches, BranchLength: raw.Length, Area: &a, To: raw.To}, nil
}

func parseGoto(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		To       *Chest `json:"to"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.To == nil {
		return Template{}, errors.New("to is required")
	}
	// The message names no coordinate: they are lab locations and must stay
	// out of logs.
	if !raw.To.inLab() {
		return Template{}, errors.New("to is outside the allowed area")
	}
	return Template{Template: raw.Template, To: raw.To}, nil
}

func parseInspect(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Chest    *Chest `json:"chest"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Chest == nil {
		return Template{}, errors.New("chest is required")
	}
	if !raw.Chest.inLab() {
		return Template{}, errors.New("chest is outside the allowed area")
	}
	return Template{Template: raw.Template, Chest: *raw.Chest}, nil
}

func (c Chest) inLab() bool {
	return c.X >= gotoMinXZ && c.X <= gotoMaxXZ && c.Z >= gotoMinXZ && c.Z <= gotoMaxXZ && c.Y >= gotoMinY && c.Y <= gotoMaxY
}

func parseObserve(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Box      *struct {
			Min *Chest `json:"min"`
			Max *Chest `json:"max"`
		} `json:"box"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Box == nil || raw.Box.Min == nil || raw.Box.Max == nil {
		return Template{}, errors.New("box with min and max is required")
	}
	// The message names no coordinate, as goto's does not.
	if !raw.Box.Min.inLab() || !raw.Box.Max.inLab() {
		return Template{}, errors.New("box is outside the allowed area")
	}
	b := Box{Min: *raw.Box.Min, Max: *raw.Box.Max}
	if err := b.validate(); err != nil {
		return Template{}, err
	}
	return Template{Template: raw.Template, Box: &b}, nil
}

func parsePlace(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Block    *Chest `json:"block"`
		Item     string `json:"item"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Block == nil {
		return Template{}, errors.New("block is required")
	}
	// The message names no coordinate: they are lab locations and must stay
	// out of logs.
	if !raw.Block.inLab() {
		return Template{}, errors.New("block is outside the allowed area")
	}
	if !validItemName.MatchString(raw.Item) {
		return Template{}, errors.New("item must be 1 to 32 characters of a-z, 0-9 and _")
	}
	return Template{Template: raw.Template, Block: raw.Block, Item: raw.Item}, nil
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
	seen := map[Chest]bool{}
	for _, c := range raw.From {
		if c == *raw.To {
			return Template{}, errors.New("to must not be one of from")
		}
		if seen[c] {
			return Template{}, errors.New("from lists a chest twice")
		}
		seen[c] = true
	}
	return Template{Template: raw.Template, Qty: total, Items: raw.Items, From: raw.From, To: raw.To}, nil
}

// Expand lists the skills a template stands for, in order.
func Expand(t Template, world skill.ChestWorld, self func() belief.Snapshot, opts ...skill.Option) ([]skill.Skill, error) {
	if t.Template == shaftTemplate && t.Entrance != nil && t.Area != nil && t.To != nil {
		sk, err := skill.NewMineShaft(world, self, skill.ShaftSpec{
			Entrance: protocol.BlockPos{t.Entrance.X, t.Entrance.Y, t.Entrance.Z},
			Dir:      facings[t.Facing],
			Depth:    t.Depth, Branches: t.Branches, BranchLength: t.BranchLength,
			Area: skill.Area{
				Min: protocol.BlockPos{t.Area.Min.X, t.Area.Min.Y, t.Area.Min.Z},
				Max: protocol.BlockPos{t.Area.Max.X, t.Area.Max.Y, t.Area.Max.Z},
			},
			Chest: protocol.BlockPos{t.To.X, t.To.Y, t.To.Z},
		}, opts...)
		if err != nil {
			return nil, err
		}
		return []skill.Skill{sk}, nil
	}
	if t.Template == haulTemplate && t.To != nil {
		sources := make([]protocol.BlockPos, len(t.From))
		for i, c := range t.From {
			sources[i] = protocol.BlockPos{c.X, c.Y, c.Z}
		}
		dest := protocol.BlockPos{t.To.X, t.To.Y, t.To.Z}
		return []skill.Skill{skill.NewHaul(world, self, sources, dest, t.Items, opts...)}, nil
	}
	if t.Template == inspectTemplate {
		return []skill.Skill{skill.NewInspect(world, self, protocol.BlockPos{t.Chest.X, t.Chest.Y, t.Chest.Z})}, nil
	}
	if t.Template == observeTemplate && t.Box != nil {
		return []skill.Skill{skill.NewObserve(world, self,
			protocol.BlockPos{t.Box.Min.X, t.Box.Min.Y, t.Box.Min.Z},
			protocol.BlockPos{t.Box.Max.X, t.Box.Max.Y, t.Box.Max.Z})}, nil
	}
	if t.Template == gotoTemplate && t.To != nil {
		goal := protocol.BlockPos{t.To.X, t.To.Y, t.To.Z}
		return []skill.Skill{skill.NewNavigate(world, self, goal, gotoWithin)}, nil
	}
	if t.Template == placeTemplate && t.Block != nil {
		goal := protocol.BlockPos{t.Block.X, t.Block.Y, t.Block.Z}
		return []skill.Skill{skill.NewPlaceBlock(world, self, goal, t.Item, opts...)}, nil
	}
	src, ok := recipes[t.Item]
	if t.Template != gatherAndDeposit || !ok {
		return nil, errors.New("unknown template or item")
	}
	chest := protocol.BlockPos{t.Chest.X, t.Chest.Y, t.Chest.Z}
	var mineOpts []skill.MineOption
	if t.Area != nil {
		mineOpts = append(mineOpts, skill.InArea(skill.Area{
			Min: protocol.BlockPos{t.Area.Min.X, t.Area.Min.Y, t.Area.Min.Z},
			Max: protocol.BlockPos{t.Area.Max.X, t.Area.Max.Y, t.Area.Max.Z},
		}))
	}
	gather := skill.NewMineBlockWith(world, self, src.Block, t.Item, src.Tool, t.Qty, mineRadius, mineOpts...)
	if src.Fell {
		gather = skill.NewFell(world, self, src.Block, t.Item, src.Tool, t.Qty, mineRadius, mineOpts, opts...)
	}
	return []skill.Skill{gather, skill.NewDepositChest(world, self, chest, t.Item, t.Qty, opts...)}, nil
}
