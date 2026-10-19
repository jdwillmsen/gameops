package skill

import (
	"context"
	"errors"
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
	idOakLog      = 5
	idWoodenPick  = 312
	idGravel      = 6
	idCoal        = 7
	chestHash     = 3162850062
	blockHash     = 777
)

var itemTable = []protocol.ItemEntry{
	{Name: "minecraft:dirt", RuntimeID: idDirt},
	{Name: "minecraft:cobblestone", RuntimeID: idCobblestone},
	{Name: "minecraft:oak_log", RuntimeID: idOakLog},
	{Name: "minecraft:gravel", RuntimeID: idGravel},
	{Name: "minecraft:coal", RuntimeID: idCoal},
	{Name: "minecraft:wooden_pickaxe", RuntimeID: idWoodenPick},
	{Name: "minecraft:iron_axe", RuntimeID: idAxe},
	{Name: "minecraft:wooden_sword", RuntimeID: 300},
	{Name: "minecraft:iron_sword", RuntimeID: 301},
	{Name: "minecraft:bread", RuntimeID: 340},
	{Name: "minecraft:cooked_beef", RuntimeID: 341},
	{Name: "minecraft:stone_sword", RuntimeID: 343},
}

// simWorld is the block map the skills and the simulated server share.
type simWorld map[protocol.BlockPos]string

func (w simWorld) Block(p protocol.BlockPos) (string, bool) { n, ok := w[p]; return n, ok }

func (w simWorld) BlockHash(p protocol.BlockPos) (uint32, bool) {
	switch name := w[p]; name {
	case "chest":
		return chestHash, true
	case "", "air":
		return 0, false
	default:
		return blockHash, true
	}
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

var errWire = errors.New("wire down")

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
	// cleared lists every cell the server emptied, in order, so a test can
	// check what the unit opened.
	cleared []protocol.BlockPos
	falls   []fall
	aborts  int
	held    int // last MobEquipment hotbar slot, -1 before one

	// chest is the chest every test that never names a position talks to;
	// chests holds the others, so a haul can have several.
	chest       *[27]slotStack
	chests      map[protocol.BlockPos]*[27]slotStack
	opened      *[27]slotStack
	fills       []timedFill
	takeStatus  []uint8 // answers to the next withdraws, in order
	takes       int
	places      int
	placed      int // blocks the server accepted on a top face
	maxRise     float32
	chestAt     protocol.BlockPos
	window      byte
	opens       int
	requests    int
	chestClosed bool

	refusePickups bool
	stallBreaks   bool
	// openDelay holds the ContainerOpen back this many inputs after the click.
	openDelay     int
	openIn        int
	openFor       protocol.BlockPos
	staleSlots    map[int]slotStack // chest slots the server has but the content hides
	failWriteAt   int               // writes fail once this many inputs were sent
	ignoreOpen    bool
	dropResponses bool
	rejectWith    uint8
	correctAt     int
	correctBy     mgl32.Vec3
}

// timedFill puts a stack in a chest slot once the unit has sent that many
// inputs, as another unit's deposit would.
type timedFill struct {
	tick  int
	chest *[27]slotStack
	slot  int
	stack slotStack
}

// fall is a block that was left unsupported and lands a few ticks later.
type fall struct {
	at   protocol.BlockPos
	left int
}

type breakSession struct {
	pos  protocol.BlockPos
	left int
}

func newRig(t *testing.T, w simWorld, feet mgl32.Vec3) *rig {
	t.Helper()
	r := &rig{t: t, world: w, nextID: 100, held: -1, window: 2, chestClosed: true, chest: new([27]slotStack), chests: map[protocol.BlockPos]*[27]slotStack{}}
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
	if r.failWriteAt > 0 && r.ticks >= r.failWriteAt {
		return errWire
	}
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

func (r *rig) PlaceOnTop(against protocol.BlockPos, hotbar int, held belief.Item, hash uint32) error {
	err := r.Actuator.PlaceOnTop(against, hotbar, held, hash)
	r.pump()
	return err
}

func (r *rig) Deposit(from belief.Item, count int, to byte) (int32, error) {
	id, err := r.Actuator.Deposit(from, count, to)
	r.pump()
	return id, err
}

func (r *rig) Withdraw(fromSlot byte, count int, to belief.Item, toSlot int) (int32, error) {
	id, err := r.Actuator.Withdraw(fromSlot, count, to, toSlot)
	r.pump()
	return id, err
}

// chestFor returns the chest at a position, creating it empty.
func (r *rig) chestFor(pos protocol.BlockPos) *[27]slotStack {
	if pos == chestPos {
		return r.chest
	}
	c, ok := r.chests[pos]
	if !ok {
		c = new([27]slotStack)
		r.chests[pos] = c
	}
	return c
}

// fillAt schedules a stack to appear in a chest after the unit has sent tick inputs.
func (r *rig) fillAt(tick int, pos protocol.BlockPos, slot int, s slotStack) {
	r.fills = append(r.fills, timedFill{tick, r.chestFor(pos), slot, s})
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

	due := r.fills[:0]
	for _, f := range r.fills {
		if r.ticks < f.tick {
			due = append(due, f)
			continue
		}
		f.chest[f.slot] = f.stack
		if !r.chestClosed && r.opened == f.chest {
			r.send(&packet.InventorySlot{WindowID: uint32(r.window), Slot: uint32(f.slot), NewItem: instance(f.stack)})
		}
	}
	r.fills = due

	if acts, ok := p.BlockActions.Value(); ok {
		for _, a := range acts {
			switch a.Action {
			case protocol.PlayerActionStartBreak:
				eye := r.Actuator.Pose().Feet.Add(mgl32.Vec3{0, 1.62, 0})
				if centre := (mgl32.Vec3{float32(a.BlockPos.X()) + 0.5, float32(a.BlockPos.Y()) + 0.5, float32(a.BlockPos.Z()) + 0.5}); centre.Sub(eye).Len() > 5.5 {
					continue
				}
				r.breaking = &breakSession{pos: a.BlockPos, left: 23}
			case protocol.PlayerActionAbortBreak:
				r.breaking = nil
				r.aborts++
			}
		}
	}
	if b := r.breaking; b != nil {
		if b.left--; b.left == 0 && !r.stallBreaks {
			r.onBroken(b.pos)
			r.breaking = nil
		}
	}
	if r.openIn > 0 {
		if r.openIn--; r.openIn == 0 {
			r.openChestNow(r.openFor)
		}
	}

	live := r.falls[:0]
	for _, f := range r.falls {
		if f.left--; f.left > 0 {
			live = append(live, f)
			continue
		}
		r.settle(f.at)
	}
	r.falls = live

	pose := r.Actuator.Pose()
	kept := r.drops[:0]
	for _, d := range r.drops {
		// The server keeps offering a drop to anyone who comes within range
		// until it despawns, so a drop missed at first can still be taken.
		if d.age++; d.age >= 10 {
			dx, dz := float64(d.at.X()-pose.Feet.X()), float64(d.at.Z()-pose.Feet.Z())
			if !r.refusePickups && math.Hypot(dx, dz) <= 1.5 && math.Abs(float64(d.at.Y()-pose.Feet.Y())) <= 1 {
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
	r.cleared = append(r.cleared, pos)
	if falling(r.world[protocol.BlockPos{pos.X(), pos.Y() + 1, pos.Z()}]) {
		r.falls = append(r.falls, fall{at: pos, left: 4})
	}
	drops := map[string]int32{"stone": idCobblestone, "oak_log": idOakLog, "dirt": idDirt, "cobblestone": idCobblestone, "gravel": idGravel, "coal_ore": idCoal}
	if item, ok := drops[name]; ok {
		// An item falls to the first solid block under it.
		y := pos.Y()
		for ; y > -64; y-- {
			if n := r.world[protocol.BlockPos{pos.X(), y - 1, pos.Z()}]; n != "air" {
				break
			}
		}
		at := mgl32.Vec3{float32(pos.X()) + 0.5, float32(y), float32(pos.Z()) + 0.5}
		r.drops = append(r.drops, drop{at: at, item: item})
	}
}

func falling(name string) bool { return name == "gravel" || name == "sand" }

// settle drops the falling blocks above a emptied cell by one, as gravity
// does: the block lands in the cell and the column above follows.
func (r *rig) settle(at protocol.BlockPos) {
	for y := at.Y(); ; y++ {
		cell, above := protocol.BlockPos{at.X(), y, at.Z()}, protocol.BlockPos{at.X(), y + 1, at.Z()}
		if r.world[cell] != "air" || !falling(r.world[above]) {
			return
		}
		r.world[cell], r.world[above] = r.world[above], "air"
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
	if r.world[u.BlockPosition] != "chest" {
		r.onPlace(u)
		return
	}
	eye := r.Actuator.Pose().Feet.Add(mgl32.Vec3{0, 1.62, 0})
	centre := mgl32.Vec3{float32(u.BlockPosition.X()) + 0.5, float32(u.BlockPosition.Y()) + 0.5, float32(u.BlockPosition.Z()) + 0.5}
	hash, _ := r.world.BlockHash(u.BlockPosition)
	if centre.Sub(eye).Len() > 6 || u.BlockRuntimeID != hash || hash == 0 {
		return
	}
	if r.openDelay > 0 {
		r.openIn, r.openFor = r.openDelay, u.BlockPosition
		return
	}
	r.openChestNow(u.BlockPosition)
}

// onPlace is the server placing a block on a top face. It refuses what the
// real one does: an air or unknown target, a cell the unit's body overlaps, a
// click out of reach or a held stack that is not the one named.
func (r *rig) onPlace(u *protocol.UseItemTransactionData) {
	blocks := map[int32]string{idDirt: "dirt", idCobblestone: "cobblestone"}
	pose := r.Actuator.Pose()
	at := protocol.BlockPos{u.BlockPosition.X(), u.BlockPosition.Y() + 1, u.BlockPosition.Z()}
	stack := &r.inv[u.HotBarSlot]
	name, placeable := blocks[stack.item]
	if u.BlockFace != 1 || !placeable || stack.count == 0 || u.HeldItem.Stack.NetworkID != stack.item ||
		r.world[u.BlockPosition] == "" || r.world[u.BlockPosition] == "air" || r.world[at] != "air" {
		return
	}
	if hash, _ := r.world.BlockHash(u.BlockPosition); u.BlockRuntimeID != hash {
		return
	}
	overlapX := math.Abs(float64(pose.Feet.X())-float64(at.X())-0.5) < 0.8
	overlapZ := math.Abs(float64(pose.Feet.Z())-float64(at.Z())-0.5) < 0.8
	if overlapX && overlapZ && pose.Feet.Y() < float32(at.Y())+1 && pose.Feet.Y()+1.8 > float32(at.Y()) {
		return
	}
	r.world[at] = name
	r.placed++
	if rise := pose.Feet.Y() - float32(at.Y()); rise > r.maxRise {
		r.maxRise = rise
	}
	stack.count--
	if stack.count == 0 {
		*stack = slotStack{}
	}
	r.send(&packet.InventorySlot{WindowID: 0, Slot: uint32(u.HotBarSlot), NewItem: instance(*stack)})
}

func (r *rig) openChestNow(pos protocol.BlockPos) {
	r.opens++
	r.window += 5
	r.chestAt = pos
	r.opened = r.chestFor(pos)
	r.chestClosed = false
	r.send(&packet.ContainerOpen{WindowID: r.window, ContainerPosition: pos, ContainerEntityUniqueID: -1})
	content := make([]protocol.ItemInstance, len(r.opened))
	for i, s := range r.opened {
		content[i] = instance(s)
	}
	r.send(&packet.InventoryContent{WindowID: uint32(r.window), Content: content})
	for i, st := range r.staleSlots {
		r.opened[i] = st
	}
}

func (r *rig) onRequest(p *packet.ItemStackRequest) {
	r.requests++
	if r.dropResponses {
		return
	}
	for _, req := range p.Requests {
		if tk, ok := req.Actions[0].(*protocol.TakeStackRequestAction); ok {
			r.onTake(req.RequestID, tk)
			continue
		}
		pl, ok := req.Actions[0].(*protocol.PlaceStackRequestAction)
		if !ok {
			continue
		}
		src, dst := &r.inv[pl.Source.Slot], &r.opened[pl.Destination.Slot]
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
		r.places++
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

// onTake answers a withdraw the way the lab server does: the chest slot's
// update, then a response naming both slots, with no update for the unit's
// own slot. A whole stack keeps its ID; a split one gets a new ID.
func (r *rig) onTake(id int32, tk *protocol.TakeStackRequestAction) {
	r.takes++
	reply := func(status uint8) {
		r.send(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{Status: status, RequestID: id}}})
	}
	if len(r.takeStatus) > 0 {
		status := r.takeStatus[0]
		r.takeStatus = r.takeStatus[1:]
		if status != 0 {
			reply(status)
			return
		}
	}
	src, dst := &r.opened[tk.Source.Slot], &r.inv[tk.Destination.Slot]
	n := int(tk.Count)
	switch {
	case chestClosedFor(r) || src.count == 0 || src.id != tk.Source.StackNetworkID || src.count < n:
		reply(act.StatusBadSource)
		return
	case dst.count > 0 && (dst.item != src.item || dst.id != tk.Destination.StackNetworkID || dst.count+n > 64):
		reply(act.StatusOccupied)
		return
	}
	moved := slotStack{src.item, n, src.id}
	if n < src.count {
		r.nextID++
		moved.id = r.nextID
	}
	if dst.count > 0 {
		moved = slotStack{dst.item, dst.count + n, dst.id}
	}
	*dst = moved
	src.count -= n
	if src.count == 0 {
		*src = slotStack{}
	}
	r.send(&packet.InventorySlot{WindowID: uint32(r.window), Slot: uint32(tk.Source.Slot), NewItem: instance(*src)})
	r.send(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{
		RequestID: id,
		ContainerInfo: []protocol.StackResponseContainerInfo{
			{Container: protocol.FullContainerName{ContainerID: protocol.ContainerLevelEntity},
				SlotInfo: []protocol.StackResponseSlotInfo{{Slot: tk.Source.Slot, Count: byte(src.count), StackNetworkID: src.id}}},
			{Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
				SlotInfo: []protocol.StackResponseSlotInfo{{Slot: tk.Destination.Slot, Count: byte(dst.count), StackNetworkID: dst.id}}},
		},
	}}})
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
