// Package repair restores the Colosseum after a fight. It compares the blocks
// the generator planned with the blocks a scan found, and emits an ordered,
// idempotent list of console commands that changes only the cells that differ,
// all inside the arena footprint, plus a report of the damage it found.
//
// It never reads the server itself: the live state comes through a Reader, so
// the same code serves a snapshot file today and a unit's loaded chunks later.
// Entities and container contents (armour stands, chests) are out of scope;
// the generator's decor.cmds owns them.
package repair
