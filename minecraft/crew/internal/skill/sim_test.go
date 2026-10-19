package skill

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// Item IDs and the chest hash are the lab server's, as the spike recorded them.
const (
	idDirt        = 3
	idCobblestone = 4
	idWoodenPick  = 312
	chestHash     = 3162850062
)

var itemTable = []protocol.ItemEntry{
	{Name: "minecraft:dirt", RuntimeID: idDirt},
	{Name: "minecraft:cobblestone", RuntimeID: idCobblestone},
	{Name: "minecraft:wooden_pickaxe", RuntimeID: idWoodenPick},
}

// simWorld is the block map the skills and the simulated server share.
type simWorld map[protocol.BlockPos]string

func (w simWorld) Block(p protocol.BlockPos) (string, bool) { n, ok := w[p]; return n, ok }

func (w simWorld) BlockHash(p protocol.BlockPos) (uint32, bool) {
	if w[p] == "chest" {
		return chestHash, true
	}
	return 0, false
}

// platform lays a dirt floor at y=0 over [x0,x1]x[z0,z1] with air above it.
func (w simWorld) platform(x0, x1, z0, z1 int32) {
	for x := x0; x <= x1; x++ {
		for z := z0; z <= z1; z++ {
			w[protocol.BlockPos{x, 0, z}] = "dirt"
			for y := int32(1); y <= 6; y++ {
				w[protocol.BlockPos{x, y, z}] = "air"
			}
		}
	}
}

// fill replaces the cells of a box.
func (w simWorld) fill(name string, x0, y0, z0, x1, y1, z1 int32) {
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for z := z0; z <= z1; z++ {
				w[protocol.BlockPos{x, y, z}] = name
			}
		}
	}
}

func (w simWorld) count(name string) int {
	n := 0
	for _, b := range w {
		if b == name {
			n++
		}
	}
	return n
}

type slotStack struct {
	item  int32
	count int
	id    int32
}

type drop struct {
	at   mgl32.Vec3
	item int32
	age  int
}

// rig is a real actuator and belief tracker wired to a simulated server. The
// server breaks a block 23 ticks after a Start, drops its item, picks the drop
// up ten ticks later within 1.5 blocks, opens chests and answers item
// requests, so the skills run against the same packets they would in a
// session. Its faults are switches the tests set.
type rig struct {
	*act.Actuator
	t       *testing.T
	world   simWorld
	tracker *belief.Tracker

	pending []packet.Packet
	inv     [36]slotStack
	nextID  int32

	lastTick uint64
	ticks    int // PlayerAuthInputs seen
	breaking *breakSession
	drops    []drop
	breaks   int
	aborts   int
	held     int // last MobEquipment hotbar slot, -1 before one

	chest       [27]slotStack
	chestAt     protocol.BlockPos
	window      byte
	opens       int
	requests    int
	chestClosed bool

	refusePickups bool
	ignoreOpen    bool
	dropResponses bool
	rejectWith    uint8
	correctAt     int
	correctBy     mgl32.Vec3
}

type breakSession struct {
	pos  protocol.BlockPos
	left int
}

func newRig(t *testing.T, w simWorld, feet mgl32.Vec3) *rig {
	t.Helper()
	r := &rig{t: t, world: w, nextID: 100, held: -1, window: 2, chestClosed: true}
	r.tracker = belief.New(time.Now)
	r.tracker.Spawn(1, feet.Add(mgl32.Vec3{0, 1.62, 0}), 0, 0, 0)
	r.tracker.ItemNames(itemTable)
	r.Actuator = act.New(r, 1, act.Pose{Feet: feet, OnGround: true}, path.NewSolid(w))
	r.Actuator.ItemNames(itemTable)
	return r
}

// give puts a stack in an inventory slot and tells the tracker.
func (r *rig) give(slot int, item int32, count int) {
	r.nextID++
	r.inv[slot] = slotStack{item, count, r.nextID}
	r.tracker.Apply(&packet.InventorySlot{WindowID: 0, Slot: uint32(slot), NewItem: instance(r.inv[slot])})
}

func instance(s slotStack) protocol.ItemInstance {
	if s.count == 0 {
		return protocol.ItemInstance{}
	}
	return protocol.ItemInstance{
		StackNetworkID: s.id,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: s.item}, Count: uint16(s.count)},
	}
}

func (r *rig) self() belief.Snapshot { return r.tracker.Snapshot() }

func (r *rig) have(name string) int {
	n := 0
	for _, it := range r.self().Inventory {
		if it.Name == name {
			n += it.Count
		}
	}
	return n
}

// WritePacket queues what the unit sent; the server answers once the actuator
// has released its lock, because replies call back into it.
func (r *rig) WritePacket(pk packet.Packet) error {
	r.pending = append(r.pending, pk)
	return nil
}

func (r *rig) Tick(in act.Input) error {
	err := r.Actuator.Tick(in)
	r.pump()
	return err
}

func (r *rig) OpenChest(pos protocol.BlockPos, face int32, hotbar int, held belief.Item, hash uint32) error {
	err := r.Actuator.OpenChest(pos, face, hotbar, held, hash)
	r.pump()
	return err
}

func (r *rig) Deposit(from belief.Item, count int, to byte) (int32, error) {
	id, err := r.Actuator.Deposit(from, count, to)
	r.pump()
	return id, err
}

func (r *rig) CloseContainer() error {
	err := r.Actuator.CloseContainer()
	r.pump()
	return err
}

func (r *rig) SelectHotbar(slot int, item belief.Item) error {
	err := r.Actuator.SelectHotbar(slot, item)
	r.pump()
	return err
}

func (r *rig) send(pk packet.Packet) {
	r.Actuator.Apply(pk)
	r.tracker.Apply(pk)
}

func (r *rig) pump() {
	for len(r.pending) > 0 {
		pk := r.pending[0]
		r.pending = r.pending[1:]
		switch p := pk.(type) {
		case *packet.PlayerAuthInput:
			r.onInput(p)
		case *packet.MobEquipment:
			r.held = int(p.HotBarSlot)
		case *packet.InventoryTransaction:
			r.onClick(p)
		case *packet.ItemStackRequest:
			r.onRequest(p)
		case *packet.ContainerClose:
			r.chestClosed = true
		}
	}
}

func (r *rig) onInput(p *packet.PlayerAuthInput) {
	if r.lastTick != 0 && p.Tick != r.lastTick+1 {
		r.t.Fatalf("tick %d followed %d: ticks must rise by one", p.Tick, r.lastTick)
	}
	r.lastTick = p.Tick
	r.ticks++

	if acts, ok := p.BlockActions.Value(); ok {
		for _, a := range acts {
			switch a.Action {
			case protocol.PlayerActionStartBreak:
				r.breaking = &breakSession{pos: a.BlockPos, left: 23}
			case protocol.PlayerActionAbortBreak:
				r.breaking = nil
				r.aborts++
			}
		}
	}
	if b := r.breaking; b != nil {
		if b.left--; b.left == 0 {
			r.onBroken(b.pos)
		}
	}

	pose := r.Actuator.Pose()
	kept := r.drops[:0]
	for _, d := range r.drops {
		// The server keeps offering a drop to anyone who comes within range
		// until it despawns, so a drop missed at first can still be taken.
		if d.age++; d.age >= 10 {
			dx, dz := float64(d.at.X()-pose.Feet.X()), float64(d.at.Z()-pose.Feet.Z())
			if !r.refusePickups && math.Hypot(dx, dz) <= 1.5 {
				r.pickUp(d.item)
				continue
			}
		}
		kept = append(kept, d)
	}
	r.drops = kept

	if r.correctAt > 0 && r.ticks == r.correctAt {
		pos := pose.Feet.Add(mgl32.Vec3{0, 1.62, 0}).Add(r.correctBy)
		r.send(&packet.CorrectPlayerMovePrediction{Position: pos, OnGround: true})
	}
}

func (r *rig) onBroken(pos protocol.BlockPos) {
	name := r.world[pos]
	r.world[pos] = "air"
	r.breaks++
	drops := map[string]int32{"stone": idCobblestone}
	if item, ok := drops[name]; ok {
		at := mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()), float32(pos.Z()) + 0.5}
		r.drops = append(r.drops, drop{at: at, item: item})
	}
}

// pickUp announces a collected item the way the server does: as a
// transaction on window 0, not a slot update.
func (r *rig) pickUp(item int32) {
	slot := -1
	for i, s := range r.inv {
		if s.item == item && s.count > 0 && s.count < 64 {
			slot = i
			break
		}
	}
	for i := 0; slot < 0 && i < len(r.inv); i++ {
		if r.inv[i].count == 0 {
			slot = i
		}
	}
	if slot < 0 {
		return
	}
	r.nextID++
	old := r.inv[slot]
	r.inv[slot] = slotStack{item, old.count + 1, r.nextID}
	r.send(&packet.InventoryTransaction{
		TransactionData: &protocol.NormalTransactionData{},
		Actions: []protocol.InventoryAction{{
			SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.Option(int8(0)),
			InventorySlot: uint32(slot), OldItem: instance(old), NewItem: instance(r.inv[slot]),
		}},
	})
}

func (r *rig) onClick(p *packet.InventoryTransaction) {
	u, ok := p.TransactionData.(*protocol.UseItemTransactionData)
	if !ok || r.ignoreOpen {
		return
	}
	eye := r.Actuator.Pose().Feet.Add(mgl32.Vec3{0, 1.62, 0})
	centre := mgl32.Vec3{float32(u.BlockPosition.X()) + 0.5, float32(u.BlockPosition.Y()) + 0.5, float32(u.BlockPosition.Z()) + 0.5}
	hash, _ := r.world.BlockHash(u.BlockPosition)
	if centre.Sub(eye).Len() > 6 || u.BlockRuntimeID != hash || hash == 0 {
		return
	}
	r.opens++
	r.window += 5
	r.chestAt = u.BlockPosition
	r.chestClosed = false
	r.send(&packet.ContainerOpen{WindowID: r.window, ContainerPosition: u.BlockPosition, ContainerEntityUniqueID: -1})
	content := make([]protocol.ItemInstance, len(r.chest))
	for i, s := range r.chest {
		content[i] = instance(s)
	}
	r.send(&packet.InventoryContent{WindowID: uint32(r.window), Content: content})
}

func (r *rig) onRequest(p *packet.ItemStackRequest) {
	r.requests++
	if r.dropResponses {
		return
	}
	for _, req := range p.Requests {
		pl, ok := req.Actions[0].(*protocol.PlaceStackRequestAction)
		if !ok {
			continue
		}
		src, dst := &r.inv[pl.Source.Slot], &r.chest[pl.Destination.Slot]
		n := int(pl.Count)
		status := uint8(0)
		switch {
		case r.rejectWith != 0:
			status = r.rejectWith
		case chestClosedFor(r) || src.id != pl.Source.StackNetworkID || src.count < n:
			status = act.StatusBadSource
		case dst.count > 0 && (dst.item != src.item || dst.count+n > 64):
			status = act.StatusOccupied
		}
		if status != 0 {
			r.send(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{Status: status, RequestID: req.RequestID}}})
			continue
		}
		if dst.count == 0 {
			dst.item, dst.id = src.item, src.id
			if n < src.count {
				r.nextID++
				dst.id = r.nextID
			}
		}
		dst.count += n
		src.count -= n
		if src.count == 0 {
			*src = slotStack{}
		}
		r.send(&packet.InventorySlot{WindowID: uint32(r.window), Slot: uint32(pl.Destination.Slot), NewItem: instance(*dst)})
		r.send(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{
			RequestID: req.RequestID,
			ContainerInfo: []protocol.StackResponseContainerInfo{
				{Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
					SlotInfo: []protocol.StackResponseSlotInfo{{Slot: pl.Source.Slot, Count: byte(src.count), StackNetworkID: src.id}}},
				{Container: protocol.FullContainerName{ContainerID: protocol.ContainerLevelEntity},
					SlotInfo: []protocol.StackResponseSlotInfo{{Slot: pl.Destination.Slot, Count: byte(dst.count), StackNetworkID: dst.id}}},
			},
		}}})
	}
}

func chestClosedFor(r *rig) bool { return r.chestClosed }

// run steps a skill until it stops. It enforces the contract the runner
// relies on: a step that reports Running sent exactly one input.
func (r *rig) run(s Skill, maxTicks int) (Status, error) {
	r.t.Helper()
	for i := 0; i < maxTicks; i++ {
		before := r.ticks
		st, err := s.Step(context.Background(), r)
		sent := r.ticks - before
		if st == Running && sent != 1 {
			r.t.Fatalf("step %d: a running step sent %d inputs, want 1", i, sent)
		}
		if sent > 1 {
			r.t.Fatalf("step %d: sent %d inputs", i, sent)
		}
		if st != Running {
			return st, err
		}
	}
	r.t.Fatalf("%s still running after %d ticks", s.Name(), maxTicks)
	return Running, nil
}

type logLine struct {
	event  string
	fields map[string]any
}

type capture struct{ lines []logLine }

func (c *capture) Info(event string, fields map[string]any) {
	c.lines = append(c.lines, logLine{event, fields})
}
