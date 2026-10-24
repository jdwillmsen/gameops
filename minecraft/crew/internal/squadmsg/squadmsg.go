// Package squadmsg holds the squad message types a unit's client and the
// combat engine share. It is data only: no I/O, no game types, so both sides
// can import it without dragging one into the other.
//
// The shapes here mirror what crew-control's squad routes read and write on
// the wire; a field renamed here must be renamed there too, or the report
// silently becomes unknown.
package squadmsg

import "time"

// Roles in chain-of-command order. A squad leads from its fittest member by
// this order: Vanguard fronts and tanks, Striker deals damage, Ranger stays
// back. Units not in a warrior role never lead.
const (
	RoleVanguard Role = "vanguard"
	RoleStriker  Role = "striker"
	RoleRanger   Role = "ranger"
)

type Role string

// OrderKind is what a squad has been told to do. The zero value means no
// order, which is what a client reports before the first one arrives.
const (
	OrderNone    OrderKind = ""
	OrderFocus             = "focus"
	OrderHold              = "hold"
	OrderAdvance           = "advance"
	OrderRegroup           = "regroup"
	OrderRetreat           = "retreat"
)

type OrderKind string

// CalloutKind is why a unit spoke up.
const (
	CalloutLowHealth  = "low_health"
	CalloutNeedHeal   = "need_heal"
	CalloutTargetDown = "target_down"
	CalloutBossPhase  = "boss_phase"
)

type CalloutKind string

// Order is one instruction to a squad, standing until replaced. Target names
// the thing to focus; empty for orders that carry no target. Seq comes from
// crew-control's per-squad log, so receivers can drop anything older.
type Order struct {
	Kind   OrderKind `json:"kind"`
	Target string    `json:"target,omitempty"`
	By     string    `json:"by,omitempty"`
	Seq    int64     `json:"seq"`
	At     time.Time `json:"at"`
}

// Callout is one unit speaking up for the squad's benefit.
type Callout struct {
	Kind CalloutKind `json:"kind"`
	From string      `json:"from"`
	Seq  int64       `json:"seq"`
	At   time.Time   `json:"at"`
}

// Status is one unit's own report: how it is, not what it is doing.
type Status struct {
	Unit   string    `json:"unit"`
	HP     float64   `json:"hp"`
	Food   int       `json:"food"`
	Weapon string    `json:"weapon"`
	Pos    Pos       `json:"pos"`
	At     time.Time `json:"at"`
}

// Pos is a position on the wire, in world block coordinates. Kept apart from
// any game vector so the wire never depends on one.
type Pos struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	Z float32 `json:"z"`
}

// Member is one unit of the squad as the poll answer lists it. Pos is absent
// until the unit's first report, and is its last place once it has died or
// gone quiet. Fresh says it spoke inside the server's leader grace.
type Member struct {
	Unit  string `json:"unit"`
	Role  Role   `json:"role"`
	Pos   *Pos   `json:"pos,omitempty"`
	Alive bool   `json:"alive"`
	Fresh bool   `json:"fresh"`
}

// Rank orders roles by who leads first: a lower rank outranks a higher
// number. An unknown role never leads, so it ranks after Ranger.
func Rank(r Role) int {
	switch r {
	case RoleVanguard:
		return 0
	case RoleStriker:
		return 1
	case RoleRanger:
		return 2
	}
	return 3
}

// ValidRole reports whether r is one of the warrior roles a unit can claim.
func ValidRole(r Role) bool { return r != "" && Rank(r) < 3 }
