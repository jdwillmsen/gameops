package act

import "github.com/sandertv/gophertunnel/minecraft/protocol"

// BreakPhase is one step of the block-breaking exchange. The server times the
// break itself, so a skill sends one Start and then waits for the block to
// turn to air.
type BreakPhase int

const (
	// BreakStart begins breaking; the server finishes the block on its own.
	BreakStart BreakPhase = iota
	// BreakContinue is optional: the break finishes without it.
	BreakContinue
	// BreakAbort cancels a break in progress. The server keeps the session
	// open after a block breaks, so only a skill that gives up needs it.
	BreakAbort
)

// BreakAction is a block action carried by one tick's input.
type BreakAction struct {
	Pos   protocol.BlockPos
	Face  int32
	Phase BreakPhase
}

func (b BreakAction) action() protocol.PlayerBlockAction {
	kind := int32(protocol.PlayerActionStartBreak)
	switch b.Phase {
	case BreakContinue:
		kind = protocol.PlayerActionContinueDestroyBlock
	case BreakAbort:
		kind = protocol.PlayerActionAbortBreak
	}
	return protocol.PlayerBlockAction{Action: kind, BlockPos: b.Pos, Face: b.Face}
}
