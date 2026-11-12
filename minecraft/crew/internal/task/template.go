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
	"slices"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	gatherAndDeposit = "gather_and_deposit"
	haulTemplate     = "haul"
	withdrawTemplate = "withdraw"
	depositTemplate  = "deposit"
	shaftTemplate    = "mine_shaft"
	maxShaftDepth    = 32
	maxBranches      = 3
	maxBranchLength  = 32
	gotoTemplate     = "goto"
	inspectTemplate  = "inspect"
	observeTemplate  = "observe_blocks"
	placeTemplate    = "place_block"
	plantTemplate    = "plant_trees"
	bedTemplate      = "use_bed"
	craftTemplate    = "craft"
	shelterTemplate  = "shelter"
	// The harness templates act on other entities, which a lab unit must never
	// be asked to do, so they exist only after EnableHarness.
	interactTemplate = "interact_entity"
	attackTemplate   = "attack_entity"
	equipTemplate    = "equip_item"
	maxEntityTimes   = 40
	defaultEvery     = 12
	// maxObserveCells caps a box at what one report can carry.
	maxObserveCells = 4096
	maxHaulItems    = 8
	maxHaulSources  = 4
	minQty          = 1
	maxQty          = 256
	// One craft task asks for at most a stack of the item.
	maxCraftCount = 64
	maxAreaSpan   = 64
	// A search box is a wider net than an area: the unit roams it looking for
	// the block, so it may span most of the lab square.
	maxSearchSpan = 400
	// plant_trees: a tree needs room, so saplings go at least minSpacing apart.
	defaultSpacing = 4
	minSpacing     = 3
	maxSpacing     = 16
	maxPlantCount  = 64
	// shelter: a wait shorter than this cannot span even the walls going up.
	minShelterTicks = 100
	// How far from the unit MineBlock looks for stone.
	mineRadius = 24
	// goto is confined to the lab's flat square, so a typo cannot send the
	// unit off to unloaded terrain.
	gotoMinXZ, gotoMaxXZ = -500, 499
	gotoMinY, gotoMaxY   = -64, 320
	// gotoWithin is how close to the cell counts as arrived.
	gotoWithin = 1.5
)

var harnessOn bool

// EnableHarness lets the entity templates parse; it is the unit's CREW_HARNESS
// setting, on only for a throwaway preview server.
func EnableHarness(on bool) { harnessOn = on }

// validItemName is the shape of an item place_block may be asked for, the
// server's item-table names: lower case, digits and underscores.
var validItemName = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

type Chest struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
	Z int32 `json:"z"`
}

// Template is a task request: gather_and_deposit fills Item, Qty and Chest, and
// optionally Area and Search; plant_trees fills Item, Qty, Area and Spacing;
// haul fills Items, From and To; withdraw and deposit fill Items and Chest;
// place_block fills Block and Item; use_bed fills Block; bootstrap fills Block
// (the table cell), To (the optional chest cell) and Search.
type Template struct {
	Template string `json:"template"`
	Item     string `json:"item"`
	Qty      int    `json:"qty"`
	Chest    Chest  `json:"chest"`
	// Block, when set, is the cell place_block fills.
	Block *Chest `json:"block,omitempty"`
	// Area, when set, confines gather_and_deposit to blocks inside the box. For
	// plant_trees it is the grove.
	Area *Area `json:"area,omitempty"`
	// Search, when set on gather_and_deposit, makes the unit look for the
	// block across this box, up to 400 blocks on an axis, before it gathers:
	// it reads the loaded blocks around it and, if the block is not there,
	// walks an expanding ring of waypoints about 32 blocks apart until it sees
	// one, then goes near it. With Search set, Area is optional; if both are
	// given Area must lie inside Search, and only blocks inside Area are
	// broken and count as found. The task fails with "no <block> found in
	// search area" once the ring has covered the box.
	Search *SearchBox `json:"search,omitempty"`
	// Spacing is plant_trees' distance between saplings, in blocks.
	Spacing int `json:"spacing,omitempty"`
	// Until and MaxTicks are shelter's: what ends the wait and the most
	// inputs to spend waiting.
	Until    string `json:"until,omitempty"`
	MaxTicks int    `json:"max_ticks,omitempty"`
	// Box, when set, is what observe_blocks reads.
	Box *Box `json:"box,omitempty"`

	// Blueprint, At and Rotation are build_blueprint's request: At is the
	// minimum corner of the stamp. DryRun is also repair_build's.
	Blueprint string `json:"blueprint,omitempty"`
	At        *Chest `json:"at,omitempty"`
	Rotation  int    `json:"rotation,omitempty"`
	DryRun    bool   `json:"dry_run,omitempty"`

	Items map[string]int `json:"items,omitempty"`
	From  []Chest        `json:"from,omitempty"`
	To    *Chest         `json:"to,omitempty"`

	Entrance     *Chest `json:"entrance,omitempty"`
	Facing       string `json:"facing,omitempty"`
	Depth        int    `json:"depth,omitempty"`
	Branches     int    `json:"branches,omitempty"`
	BranchLength int    `json:"branch_length,omitempty"`

	// Plan is repair_build's; its area is Area, and it shares DryRun.
	Plan string `json:"plan,omitempty"`

	// Target, Times and Every are the entity templates' request: Item is what
	// is held (empty for a bare hand), Times how often to act and Every the
	// ticks between acts.
	Target *EntityTarget `json:"target,omitempty"`
	Times  int           `json:"times,omitempty"`
	Every  int           `json:"every,omitempty"`
}

// EntityTarget names the entity an entity template acts on: a runtime id, or
// the nearest of a type and, when given, a name.
type EntityTarget struct {
	ID   uint64 `json:"id,omitempty"`
	Type string `json:"type,omitempty"`
	Name string `json:"name,omitempty"`
}

// facings maps a heading to its step in x and z: north is toward negative z.
var facings = map[string][2]int32{"north": {0, -1}, "south": {0, 1}, "east": {1, 0}, "west": {-1, 0}}

// itemName is the shape of a Minecraft identifier, the only item name a
// template accepts.
var itemName = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// Area is an inclusive box, min at most max on every axis.
type Area struct {
	Min Chest `json:"min"`
	Max Chest `json:"max"`
}

// SearchBox is the inclusive box a unit may roam to find a block. It is wider
// than an Area and, unlike it, must lie inside the lab square.
type SearchBox struct {
	Min Chest `json:"min"`
	Max Chest `json:"max"`
}

func (b SearchBox) validate() error {
	for _, ax := range [][2]int32{{b.Min.X, b.Max.X}, {b.Min.Y, b.Max.Y}, {b.Min.Z, b.Max.Z}} {
		if ax[0] > ax[1] {
			return errors.New("search min must not exceed max")
		}
		if int64(ax[1])-int64(ax[0])+1 > maxSearchSpan {
			return fmt.Errorf("search must span at most %d blocks on each axis", maxSearchSpan)
		}
	}
	// The message names no coordinate: they are lab locations and must stay
	// out of logs.
	if !b.Min.inLab() || !b.Max.inLab() {
		return errors.New("search is outside the allowed area")
	}
	return nil
}

func (b SearchBox) contains(a Area) bool {
	return a.Min.X >= b.Min.X && a.Max.X <= b.Max.X && a.Min.Y >= b.Min.Y && a.Max.Y <= b.Max.Y &&
		a.Min.Z >= b.Min.Z && a.Max.Z <= b.Max.Z
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
		// A type error quotes the offending value, which for a position is a
		// lab coordinate, so only the field and the wanted type are kept.
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return fmt.Errorf("invalid task: %s must be %s", typeErr.Field, typeErr.Type)
		}
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
	case withdrawTemplate, depositTemplate:
		return parseTransfer(body)
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
	case craftTemplate:
		return parseCraft(body)
	case plantTemplate:
		return parsePlant(body)
	case bedTemplate:
		return parseBed(body)
	case shelterTemplate:
		return parseShelter(body)
	case bootstrapTemplate:
		return parseBootstrap(body)
	case buildTemplate:
		return parseBuild(body)
	case repairTemplate:
		return parseRepair(body)
	case interactTemplate, attackTemplate:
		if harnessOn {
			return parseEntityUse(body)
		}
	case equipTemplate:
		if harnessOn {
			return parseEquip(body)
		}
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

var entityType = regexp.MustCompile(`^[a-z0-9_]{1,32}(:[a-z0-9_]{1,32})?$`)

func parseEntityUse(body []byte) (Template, error) {
	var raw struct {
		Template string        `json:"template"`
		Target   *EntityTarget `json:"target"`
		Item     string        `json:"item"`
		Times    int           `json:"times"`
		Every    int           `json:"every"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	t := raw.Target
	if t == nil || (t.ID == 0 && t.Type == "") {
		return Template{}, errors.New("target needs an id or a type")
	}
	if t.Type != "" && !entityType.MatchString(t.Type) && t.Type != "player" {
		return Template{}, errors.New("target type is not an entity type")
	}
	if raw.Item != "" && !itemName.MatchString(raw.Item) {
		return Template{}, errors.New("item must be 1 to 32 of a-z, 0-9 or _")
	}
	if raw.Times == 0 {
		raw.Times = 1
	}
	if raw.Times < 1 || raw.Times > maxEntityTimes {
		return Template{}, fmt.Errorf("times must be 1 to %d", maxEntityTimes)
	}
	if raw.Every == 0 {
		raw.Every = defaultEvery
	}
	if raw.Every < 1 || raw.Every > 200 {
		return Template{}, errors.New("every must be 1 to 200 ticks")
	}
	return Template{Template: raw.Template, Target: t, Item: raw.Item, Times: raw.Times, Every: raw.Every, Qty: raw.Times}, nil
}

func parseEquip(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Item     string `json:"item"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Item != "" && !itemName.MatchString(raw.Item) {
		return Template{}, errors.New("item must be 1 to 32 of a-z, 0-9 or _")
	}
	return Template{Template: raw.Template, Item: raw.Item}, nil
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
		Search *struct {
			Min *Chest `json:"min"`
			Max *Chest `json:"max"`
		} `json:"search"`
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
	if raw.Search != nil {
		if raw.Search.Min == nil || raw.Search.Max == nil {
			return Template{}, errors.New("search needs min and max")
		}
		s := SearchBox{Min: *raw.Search.Min, Max: *raw.Search.Max}
		if err := s.validate(); err != nil {
			return Template{}, err
		}
		if t.Area != nil && !s.contains(*t.Area) {
			return Template{}, errors.New("area must lie inside search")
		}
		t.Search = &s
	}
	return t, nil
}

// parseShelter reads {"template":"shelter","until":"dawn"} with an optional
// "max_ticks", how long to wait inside before leaving whatever the clock says.
// Dawn is the only thing to wait for, so until may be left out but nothing
// else is accepted, which keeps room for other conditions later.
func parseShelter(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Until    string `json:"until"`
		MaxTicks int    `json:"max_ticks"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Until != "" && raw.Until != "dawn" {
		return Template{}, errors.New("until must be dawn")
	}
	if raw.MaxTicks == 0 {
		raw.MaxTicks = skill.DefaultShelterTicks
	}
	if raw.MaxTicks < minShelterTicks || raw.MaxTicks > skill.MaxShelterTicks {
		return Template{}, fmt.Errorf("max_ticks must be %d to %d", minShelterTicks, skill.MaxShelterTicks)
	}
	return Template{Template: raw.Template, Until: "dawn", MaxTicks: raw.MaxTicks}, nil
}

// plantable is the closed set of items plant_trees sets out.
var plantable = map[string]bool{"oak_sapling": true}

// parsePlant reads {"template":"plant_trees","item":"oak_sapling","area":{...},
// "spacing":4,"qty":N}. Qty is the most saplings to plant; the grove may take
// fewer.
func parsePlant(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Item     string `json:"item"`
		Qty      int    `json:"qty"`
		Spacing  int    `json:"spacing"`
		Area     *struct {
			Min *Chest `json:"min"`
			Max *Chest `json:"max"`
		} `json:"area"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if !plantable[raw.Item] {
		return Template{}, errors.New("unknown item")
	}
	if raw.Qty < minQty || raw.Qty > maxPlantCount {
		return Template{}, fmt.Errorf("qty must be %d to %d", minQty, maxPlantCount)
	}
	if raw.Spacing == 0 {
		raw.Spacing = defaultSpacing
	}
	if raw.Spacing < minSpacing || raw.Spacing > maxSpacing {
		return Template{}, fmt.Errorf("spacing must be %d to %d", minSpacing, maxSpacing)
	}
	if raw.Area == nil || raw.Area.Min == nil || raw.Area.Max == nil {
		return Template{}, errors.New("area with min and max is required")
	}
	a := Area{Min: *raw.Area.Min, Max: *raw.Area.Max}
	if err := a.validate(); err != nil {
		return Template{}, err
	}
	if !a.Min.inLab() || !a.Max.inLab() {
		return Template{}, errors.New("area is outside the allowed area")
	}
	return Template{Template: raw.Template, Item: raw.Item, Qty: raw.Qty, Area: &a, Spacing: raw.Spacing}, nil
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

// parseBed reads {"template":"use_bed","block":{x,y,z}}.
func parseBed(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Block    *Chest `json:"block"`
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
	return Template{Template: raw.Template, Block: raw.Block}, nil
}

// parseCraft reads {"template":"craft","item":"<name>","count":N}. The count
// lands in Qty, which is the field the task record reports as progress.
func parseCraft(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Item     string `json:"item"`
		Count    int    `json:"count"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if !itemName.MatchString(raw.Item) {
		return Template{}, errors.New("item must be 1 to 32 of a-z, 0-9 or _")
	}
	if raw.Count < minQty || raw.Count > maxCraftCount {
		return Template{}, fmt.Errorf("count must be %d to %d", minQty, maxCraftCount)
	}
	// Whether a recipe exists for the item is the server's to say, so the
	// template only checks the shape of the name.
	return Template{Template: raw.Template, Item: raw.Item, Qty: raw.Count}, nil
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

// parseTransfer reads {"template":"withdraw|deposit","chest":{...},"items":{...}},
// the one-chest counterpart to haul: named items with counts across a single
// chest instead of several. Items may be any server name, not just the
// craftable set, so a unit can be sent to fetch a tool.
func parseTransfer(body []byte) (Template, error) {
	var raw struct {
		Template string         `json:"template"`
		Chest    *Chest         `json:"chest"`
		Items    map[string]int `json:"items"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Chest == nil {
		return Template{}, errors.New("chest is required")
	}
	// The message names no coordinate: they are lab locations and must stay
	// out of logs.
	if !raw.Chest.inLab() {
		return Template{}, errors.New("chest is outside the allowed area")
	}
	if len(raw.Items) < 1 || len(raw.Items) > maxHaulItems {
		return Template{}, fmt.Errorf("items must list %d to %d items", 1, maxHaulItems)
	}
	total := 0
	for item, qty := range raw.Items {
		total += qty
		if !itemName.MatchString(item) {
			return Template{}, errors.New("item must be 1 to 32 of a-z, 0-9 or _")
		}
		if qty < minQty || qty > maxQty {
			return Template{}, fmt.Errorf("qty must be %d to %d", minQty, maxQty)
		}
	}
	return Template{Template: raw.Template, Chest: *raw.Chest, Items: raw.Items, Qty: total}, nil
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
	if t.Template == withdrawTemplate && len(t.Items) > 0 {
		chest := protocol.BlockPos{t.Chest.X, t.Chest.Y, t.Chest.Z}
		return []skill.Skill{skill.NewWithdrawChest(world, self, chest, t.Items, opts...)}, nil
	}
	if t.Template == depositTemplate && len(t.Items) > 0 {
		chest := protocol.BlockPos{t.Chest.X, t.Chest.Y, t.Chest.Z}
		// Sorted so the skill list, and the task record's skill field with it,
		// is the same for every run of the same request.
		names := make([]string, 0, len(t.Items))
		for n := range t.Items {
			names = append(names, n)
		}
		slices.Sort(names)
		out := make([]skill.Skill, 0, len(names))
		for _, n := range names {
			out = append(out, skill.NewDepositChest(world, self, chest, n, t.Items[n], opts...))
		}
		return out, nil
	}
	if t.Template == inspectTemplate {
		return []skill.Skill{skill.NewInspect(world, self, protocol.BlockPos{t.Chest.X, t.Chest.Y, t.Chest.Z})}, nil
	}
	if t.Template == observeTemplate && t.Box != nil {
		return []skill.Skill{skill.NewObserve(world, self,
			protocol.BlockPos{t.Box.Min.X, t.Box.Min.Y, t.Box.Min.Z},
			protocol.BlockPos{t.Box.Max.X, t.Box.Max.Y, t.Box.Max.Z})}, nil
	}
	if t.Template == buildTemplate && t.At != nil {
		return expandBuild(t, world, self, opts)
	}
	if t.Template == repairTemplate {
		return expandRepair(t, world, self, opts...)
	}
	if (t.Template == interactTemplate || t.Template == attackTemplate) && t.Target != nil {
		kind := skill.Interact
		if t.Template == attackTemplate {
			kind = skill.Hit
		}
		ref := skill.EntityRef{ID: t.Target.ID, Type: t.Target.Type, Name: t.Target.Name}
		return []skill.Skill{skill.NewEntityUse(world, self, ref, kind, t.Item, t.Times, t.Every, opts...)}, nil
	}
	if t.Template == equipTemplate {
		return []skill.Skill{skill.NewEquip(self, t.Item)}, nil
	}
	if t.Template == bootstrapTemplate && t.Block != nil {
		return expandBootstrap(t, world, self, opts), nil
	}
	if t.Template == craftTemplate {
		return []skill.Skill{skill.NewCraft(world, self, t.Item, t.Qty)}, nil
	}
	if t.Template == plantTemplate && t.Area != nil {
		return []skill.Skill{skill.NewPlant(world, self, t.Item, skillArea(t.Area), t.Spacing, t.Qty, opts...)}, nil
	}
	if t.Template == shelterTemplate {
		return []skill.Skill{skill.NewShelter(world, self, t.MaxTicks, opts...)}, nil
	}
	if t.Template == gotoTemplate && t.To != nil {
		goal := protocol.BlockPos{t.To.X, t.To.Y, t.To.Z}
		return []skill.Skill{skill.NewNavigate(world, self, goal, gotoWithin)}, nil
	}
	if t.Template == placeTemplate && t.Block != nil {
		goal := protocol.BlockPos{t.Block.X, t.Block.Y, t.Block.Z}
		return []skill.Skill{skill.NewPlaceBlock(world, self, goal, t.Item, opts...)}, nil
	}
	if t.Template == bedTemplate && t.Block != nil {
		return []skill.Skill{skill.NewUseBed(world, self, protocol.BlockPos{t.Block.X, t.Block.Y, t.Block.Z}, opts...)}, nil
	}
	src, ok := recipes[t.Item]
	if t.Template != gatherAndDeposit || !ok {
		return nil, errors.New("unknown template or item")
	}
	chest := protocol.BlockPos{t.Chest.X, t.Chest.Y, t.Chest.Z}
	var mineOpts []skill.MineOption
	var accept *skill.Area
	if t.Area != nil {
		a := skillArea(t.Area)
		accept = &a
		mineOpts = append(mineOpts, skill.InArea(a))
	}
	var box *skill.Area
	if t.Search != nil {
		box = &skill.Area{
			Min: protocol.BlockPos{t.Search.Min.X, t.Search.Min.Y, t.Search.Min.Z},
			Max: protocol.BlockPos{t.Search.Max.X, t.Search.Max.Y, t.Search.Max.Z},
		}
	}
	if src.Chancy {
		mineOpts = append(mineOpts, skill.Chancy(skill.ChancyMisses))
		// A leaf that cannot be reached from here is not the end of the
		// search: the unit looks further, as it does for a log.
		if box != nil {
			mineOpts = append(mineOpts, skill.Seeking(*box, accept))
		}
	}
	gather := skill.NewMineBlockWith(world, self, src.Block, t.Item, src.Tool, t.Qty, mineRadius, mineOpts...)
	if src.Fell {
		gather = skill.NewFell(world, self, src.Block, t.Item, src.Tool, t.Qty, mineRadius, mineOpts, opts...)
	}
	out := []skill.Skill{gather}
	if sapling, ok := saplingFor(src.Block); ok && src.Fell {
		// The grove regrows only if what is cut is replanted; saplings the
		// unit carries go back at the foot of each trunk.
		if cut, ok := gather.(skill.Cutter); ok {
			out = append(out, skill.NewReplant(world, self, cut, sapling, opts...))
		}
	}
	out = append(out, skill.NewDepositChest(world, self, chest, t.Item, t.Qty, opts...))
	if box != nil {
		out = append([]skill.Skill{skill.NewExplore(world, self, src.Block, *box, accept)}, out...)
	}
	return out, nil
}

// saplingFor names the sapling a kind of log grows from.
func saplingFor(log string) (string, bool) {
	kind, ok := strings.CutSuffix(log, "_log")
	if !ok {
		return "", false
	}
	return kind + "_sapling", true
}

func skillArea(a *Area) skill.Area {
	return skill.Area{
		Min: protocol.BlockPos{a.Min.X, a.Min.Y, a.Min.Z},
		Max: protocol.BlockPos{a.Max.X, a.Max.Y, a.Max.Z},
	}
}
