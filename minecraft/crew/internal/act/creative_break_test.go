package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestACreativeUnitSendsOnlyTheStartOfABreak(t *testing.T) {
	target := protocol.BlockPos{3, 0, 1}
	phases := []BreakPhase{BreakStart, BreakContinue, BreakAbort}
	for _, creative := range []bool{false, true} {
		a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{3.5, 1, 0.5}})
		a.SetCreative(func() bool { return creative })
		for _, ph := range phases {
			if err := a.Tick(Input{Break: &BreakAction{Pos: target, Face: 1, Phase: ph}}); err != nil {
				t.Fatal(err)
			}
		}
		var kinds []int32
		for _, in := range s.inputs(t) {
			if hasFlag(in, packet.InputFlagPerformBlockActions) {
				acts, _ := in.BlockActions.Value()
				for _, ba := range acts {
					kinds = append(kinds, ba.Action)
				}
			}
		}
		want := []int32{protocol.PlayerActionStartBreak, protocol.PlayerActionContinueDestroyBlock, protocol.PlayerActionAbortBreak}
		if creative {
			want = want[:1]
		}
		if len(kinds) != len(want) {
			t.Fatalf("creative=%v: block actions = %v, want %v", creative, kinds, want)
		}
		for i := range want {
			if kinds[i] != want[i] {
				t.Errorf("creative=%v: block actions = %v, want %v", creative, kinds, want)
			}
			if kinds[i] == protocol.PlayerActionCreativePlayerDestroyBlock {
				t.Errorf("creative destroy action 13 was sent")
			}
		}
	}
}
