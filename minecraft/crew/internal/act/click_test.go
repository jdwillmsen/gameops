package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestClickBlockSendsTheFaceAndHitAsGiven(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	a, s := newPlacer(stoneBelow(target), mgl32.Vec3{1.5, 1, 2.5}, 2, cobbleInstance(16, 9))
	against := protocol.BlockPos{1, 0, 2}
	if err := a.ClickBlock(against, 4, mgl32.Vec3{0, 0.75, 0.5}, "cobblestone"); err != nil {
		t.Fatal(err)
	}
	u := s.last(t).(*packet.InventoryTransaction).TransactionData.(*protocol.UseItemTransactionData)
	if u.BlockPosition != against || u.BlockFace != 4 || u.ClickedPosition != (mgl32.Vec3{0, 0.75, 0.5}) ||
		u.BlockRuntimeID != 4321 || u.HotBarSlot != 2 {
		t.Errorf("use = %+v", u)
	}
}

func TestClickBlockRefusesAnUnknownSupport(t *testing.T) {
	a, _ := newPlacer(flat(), mgl32.Vec3{1.5, 1, 2.5}, 2, cobbleInstance(16, 9))
	if err := a.ClickBlock(protocol.BlockPos{1, 50, 2}, 1, mgl32.Vec3{0.5, 1, 0.5}, "cobblestone"); err == nil {
		t.Error("clicked a block the unit was never told about")
	}
}

func TestSneakGoesOutAsStartHoldAndStop(t *testing.T) {
	a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{1, 1, 2}})
	flag := func(pk packet.Packet, f int) bool { return pk.(*packet.PlayerAuthInput).InputData.Load(f) }
	for _, sneak := range []bool{false, true, true, false} {
		if err := a.Tick(Input{Sneak: sneak}); err != nil {
			t.Fatal(err)
		}
	}
	want := []struct{ start, hold, stop bool }{{false, false, false}, {true, true, false}, {false, true, false}, {false, false, true}}
	for i, w := range want {
		pk := s.got[i]
		got := struct{ start, hold, stop bool }{flag(pk, packet.InputFlagStartSneaking), flag(pk, packet.InputFlagSneaking), flag(pk, packet.InputFlagStopSneaking)}
		if got != w {
			t.Errorf("tick %d: %+v, want %+v", i, got, w)
		}
	}
}
