package main

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/blocks"
)

func (c *recordingConn) entityUses(action int32) []*protocol.UseItemOnEntityTransactionData {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*protocol.UseItemOnEntityTransactionData
	for _, pk := range c.out {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData); ok && d.ActionType == action {
				out = append(out, d)
			}
		}
	}
	return out
}

// idleRig is a spawned unit wired as run wires it, with a life that would
// defend itself: the worst case of a forgotten flag.
type idleRig struct {
	conn *recordingConn
	u    *unit
}

func startRig(t *testing.T, cfg config.Config) *idleRig {
	t.Helper()
	tracker := belief.New(time.Now)
	tracker.Spawn(7, mgl32.Vec3{0, 32768, 0}, 0, 0, 0)
	tracker.SetUniqueID(-7)
	conn := &recordingConn{}
	world := blocks.New(noNames{}, 4)
	runner := task.NewRunner(nil, nil, tracker.Snapshot, time.Minute, nopLog{})
	lf := life.New(life.Config{Radius: 8, Defend: true}, world, tracker.Snapshot, nopLog{}, rand.New(rand.NewSource(1)))
	idle := newIdleReflex(cfg, tracker, lf, idleLog{})
	runner.SetReflex(unitReflex(lf, idle))
	u := newUnit(7, tracker, world, nil, runner, lf, conn)
	if idle != nil {
		idle.Begin(7, -7)
		u.afk = idle
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go runner.Run(ctx)
	_ = u.apply(&packet.Respawn{State: packet.RespawnStateReadyToSpawn, Position: mgl32.Vec3{4, 101.62, 5}})
	return &idleRig{conn: conn, u: u}
}

type idleLog struct{}

func (idleLog) Info(string, map[string]any) {}
func (idleLog) Warn(string, map[string]any) {}

func TestOnlyIdleModeGetsTheIdleReflex(t *testing.T) {
	lf := life.New(life.Config{}, blocks.New(noNames{}, 1), belief.New(time.Now).Snapshot, nopLog{}, rand.New(rand.NewSource(1)))
	tracker := belief.New(time.Now)
	if r := newIdleReflex(config.Config{Mode: config.ModeWork}, tracker, lf, idleLog{}); r != nil {
		t.Fatal("a worker got the idle reflex")
	}
	if got := unitReflex(lf, nil); got != task.Reflex(lf) {
		t.Errorf("work mode reflex = %T, want life", got)
	}
	idle := newIdleReflex(config.Config{Mode: config.ModeIdle}, tracker, lf, idleLog{})
	if idle == nil {
		t.Fatal("an idle unit got no idle reflex")
	}
	if got := unitReflex(lf, idle); got != task.Reflex(idle) {
		t.Errorf("idle mode reflex = %T, want the idle reflex", got)
	}
}

// A zombie beside the unit is what life fights. In work mode it does; in idle
// mode life never gets the unit, whatever its flags say.
func TestIdleModeNeverFightsOrTends(t *testing.T) {
	zombie := &packet.AddActor{EntityUniqueID: 50, EntityRuntimeID: 50, EntityType: "minecraft:zombie", Position: mgl32.Vec3{5, 100, 5}}

	work := startRig(t, config.Config{Mode: config.ModeWork})
	_ = work.u.apply(zombie)
	waitFor(t, func() bool { return len(work.conn.entityUses(protocol.UseItemOnEntityActionAttack)) > 0 })

	idle := startRig(t, config.Config{Mode: config.ModeIdle})
	_ = idle.u.apply(zombie)
	_ = idle.u.apply(&packet.UpdateAttributes{EntityRuntimeID: 7, Attributes: []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: 2, Max: 20}},
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:player.hunger", Value: 1, Max: 20}},
	}})
	waitFor(t, func() bool { return idle.conn.countInputs() >= 30 })
	if hits := idle.conn.entityUses(protocol.UseItemOnEntityActionAttack); len(hits) != 0 {
		t.Fatalf("the idle unit attacked: %+v", hits)
	}
	for _, pk := range idle.conn.out {
		if _, ok := pk.(*packet.MobEquipment); ok {
			t.Fatalf("the idle unit, with stow off, changed its hand: %+v", pk)
		}
	}
}

// The seat through the real actuator: stow, aim from where the actuator
// stands, click. A pose that drifted every tick would re-aim forever.
func TestIdleUnitSitsOnTheCushionNearItsSeat(t *testing.T) {
	r := startRig(t, config.Config{Mode: config.ModeIdle, Stow: true, Seat: [3]float32{5, 100, 6}, HasSeat: true})
	_ = r.u.apply(&packet.InventoryContent{WindowID: protocol.WindowIDInventory})
	_ = r.u.apply(&packet.AddActor{EntityUniqueID: 60, EntityRuntimeID: 61, EntityType: "minecraft:cushion", Position: mgl32.Vec3{5, 100, 6}})
	waitFor(t, func() bool { return len(r.conn.entityUses(protocol.UseItemOnEntityActionInteract)) > 0 })
	d := r.conn.entityUses(protocol.UseItemOnEntityActionInteract)[0]
	if d.TargetEntityRuntimeID != 61 || d.HotBarSlot != 0 || d.HeldItem.Stack.NetworkID != 0 {
		t.Errorf("interact = %+v, want cushion 61 from the empty slot 0", d)
	}
}
