package skill

import (
	"context"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// useTransactions records the entity transactions the unit sends.
func useTransactions(r *rig) *[]*protocol.UseItemOnEntityTransactionData {
	var got []*protocol.UseItemOnEntityTransactionData
	r.onWrite = func(pk packet.Packet) bool {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData); ok {
				got = append(got, d)
			}
		}
		return false
	}
	return &got
}

func guardOf(ents ...belief.Entity) Option {
	return WithGuard(Guard{Each: func(f func(belief.Entity)) {
		for _, e := range ents {
			f(e)
		}
	}})
}

// The unit walks to the animal and right-clicks it with the named item the
// requested number of times, and nothing else.
func TestEntityUseWalksThenInteractsWithTheHeldItem(t *testing.T) {
	w := flatWorld(24)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idCobblestone, 4)
	got := useTransactions(r)
	tiger := belief.Entity{ID: 77, Type: "gameops:tiger", Pos: mgl32.Vec3{12.5, 1, 12.5}}
	sk := NewEntityUse(w, r.self, EntityRef{Type: "gameops:tiger"}, Interact, "cobblestone", 3, 4, guardOf(tiger))
	st, err := r.run(sk, 600)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if len(*got) != 3 {
		t.Fatalf("sent %d interactions, want 3", len(*got))
	}
	for _, d := range *got {
		if d.TargetEntityRuntimeID != 77 || d.ActionType != protocol.UseItemOnEntityActionInteract {
			t.Errorf("transaction %+v", d)
		}
	}
	if sk.(Progresser).Progress() != 3 {
		t.Errorf("progress %d", sk.(Progresser).Progress())
	}
}

func TestEntityUseHitsWithAttackActionAndAnEmptyHand(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idCobblestone, 4)
	got := useTransactions(r)
	bot := belief.Entity{ID: 9, Type: "player", Name: "botA", Pos: mgl32.Vec3{2.5, 1, 3.5}}
	sk := NewEntityUse(w, r.self, EntityRef{Type: "player", Name: "BOTA"}, Hit, "", 2, 3, guardOf(bot))
	if st, err := r.run(sk, 100); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if len(*got) != 2 || (*got)[0].ActionType != protocol.UseItemOnEntityActionAttack || (*got)[0].HeldItem.Stack.NetworkID != 0 {
		t.Fatalf("transactions %+v", *got)
	}
}

func TestEntityUseFailsWhenTheTargetNeverAppears(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	sk := NewEntityUse(w, r.self, EntityRef{ID: 5}, Interact, "", 1, 3, guardOf())
	st, err := r.run(sk, 200)
	if st != Failed || err == nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestEntityUseFailsWithoutTheItem(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	e := belief.Entity{ID: 5, Type: "gameops:lion", Pos: mgl32.Vec3{2.5, 1, 3.5}}
	sk := NewEntityUse(w, r.self, EntityRef{ID: 5}, Interact, "mutton", 1, 3, guardOf(e))
	if st, err := r.run(sk, 50); st != Failed || err == nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

// The last feed uses up the item and the last hit can remove the target; the
// task is still done.
func TestEntityUseIsDoneEvenIfTheTargetAndItemAreGone(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idCobblestone, 1)
	present := true
	e := belief.Entity{ID: 5, Type: "gameops:lion", Pos: mgl32.Vec3{2.5, 1, 3.5}}
	guard := WithGuard(Guard{Each: func(f func(belief.Entity)) {
		if present {
			f(e)
		}
	}})
	sk := NewEntityUse(w, r.self, EntityRef{ID: 5}, Hit, "cobblestone", 1, 3, guard)
	for i := 0; i < 20; i++ {
		st, err := sk.Step(context.Background(), r)
		if st == Done {
			return
		}
		if st == Failed {
			t.Fatalf("failed: %v", err)
		}
		if p, _ := sk.(Progresser); p.Progress() == 1 {
			present = false
			r.give(0, 0, 0)
		}
	}
	t.Fatal("never finished")
}
