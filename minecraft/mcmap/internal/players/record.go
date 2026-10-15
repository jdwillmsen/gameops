// Package players reads what the world save holds about each player: their
// vitals, what they carry, where they died and sleep. It is read from the
// mirrored copy of the world once per snapshot and never from the live
// server, so everything here is as old as the last save.
package players

import "time"

// Gauge is an attribute with its ceiling.
type Gauge struct {
	Current float64 `json:"current"`
	Max     float64 `json:"max"`
}

// Enchantment is one enchantment on an item. Name is empty for an id the
// table does not know, and the page then shows the number.
type Enchantment struct {
	ID    int    `json:"id"`
	Name  string `json:"name,omitempty"`
	Level int    `json:"level"`
}

// Item is a stack. Slot is its place in the list it came from: the
// inventory slot, or 0 to 3 head to feet for armour.
type Item struct {
	Slot int `json:"slot"`
	// Name is the item's id without the minecraft: prefix.
	Name  string `json:"name"`
	Count int    `json:"count"`
	// Damage is how much durability is used up. MaxDamage is how much the
	// item has, where the table of them knows it; the save does not say.
	Damage    int           `json:"damage,omitempty"`
	MaxDamage int           `json:"maxDamage,omitempty"`
	Custom    string        `json:"customName,omitempty"`
	Enchants  []Enchantment `json:"enchantments,omitempty"`
}

// Place is a position in a dimension.
type Place struct {
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Z         int    `json:"z"`
	Dimension string `json:"dimension"`
}

// Record is one player as the save holds them.
type Record struct {
	Health     *Gauge `json:"health,omitempty"`
	Hunger     *Gauge `json:"hunger,omitempty"`
	Saturation *Gauge `json:"saturation,omitempty"`
	Absorption *Gauge `json:"absorption,omitempty"`
	// Air is the breath left, in ticks; the most is 300.
	Air *int `json:"air,omitempty"`

	Level *int `json:"level,omitempty"`
	// Progress is how far to the next level, from 0 to 1.
	Progress *float64 `json:"progress,omitempty"`

	GameMode  string `json:"gameMode,omitempty"`
	Dimension string `json:"dimension,omitempty"`
	// Saved is where the player was when the save was written.
	Saved *Place `json:"saved,omitempty"`

	// Armor is what is worn; a slot that is empty is absent from the list,
	// and Slot says which are not.
	Armor    []Item `json:"armor"`
	Mainhand *Item  `json:"mainhand,omitempty"`
	Offhand  *Item  `json:"offhand,omitempty"`
	// Inventory is slots 0 to 8 the hotbar and 9 to 35 the rest.
	Inventory  []Item `json:"inventory"`
	EnderChest []Item `json:"enderChest"`

	Death *Place `json:"death,omitempty"`
	// Spawn is the bed or anchor the player respawns at; absent while they
	// have none, and they then go to the world's spawn.
	Spawn *Place `json:"spawn,omitempty"`

	RecipesUnlocked int  `json:"recipesUnlocked"`
	SeenCredits     bool `json:"seenCredits"`
	WardenThreat    *int `json:"wardenThreat,omitempty"`
}

// Held is a Record with when the snapshot it was read from was taken.
type Held struct {
	Record
	At time.Time `json:"-"`
}
