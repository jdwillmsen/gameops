// Package players holds the last snapshot's player records for the map's
// own routes. The record types, the decoder and the per-snapshot store are
// shared with the other services that read a world's players; the aliases
// keep this package's name stable for the map.
package players

import (
	"github.com/jdwillmsen/gameops/minecraft/pkg/playerdata"
)

type (
	Gauge       = playerdata.Gauge
	Enchantment = playerdata.Enchantment
	Item        = playerdata.Item
	Place       = playerdata.Place
	Record      = playerdata.Record
	Stats       = playerdata.Stats
	Store       = playerdata.Store
)

// Decode reads one player's record; see playerdata.Decode.
var Decode = playerdata.Decode
