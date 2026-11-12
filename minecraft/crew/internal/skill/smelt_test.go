package skill

import (
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

var furnacePos = protocol.BlockPos{12, 1, 10}

// furnaceSim is a furnace the rig's server does not know about: it opens a
// window on a click, moves stacks as item stack requests ask, cooks one item
// per cookTicks while it has fuel and shuts when told. It answers from the
// rig's tick hook, outside the actuator's lock, because replies call back into
// it.
type furnaceSim struct {
	r      *rig
	pos    protocol.BlockPos
	slots  [3]slotStack
	window byte
	open   bool
	queue  []packet.Packet
	// Cooking state: ticks into the current item and ticks of fuel left.
	progress int
	burn     int
	// ignoreOpens swallows the first n clicks; refuse answers every request
	// with this status when non-zero.
	ignoreOpens int
	refuse      uint8
	// stall stops the furnace cooking, as one with no fuel does.
	stall   bool
	clicks  int
	opens   int
	cooked  int
	recipes map[int32]int32
	fuels   map[int32]int
}

func newFurnaceSim(r *rig) *furnaceSim {
	f := &furnaceSim{r: r, pos: furnacePos, window: 30,
		recipes: map[int32]int32{idRawIron: idIronIngot},
		fuels:   map[int32]int{idCoal: 1600, idCharcoal: 1600, idOakPlanks: 300}}
	r.world[furnacePos] = "furnace"
	r.onWrite = f.intercept
	r.onTick = f.tick
	return f
}

func (f *furnaceSim) intercept(pk packet.Packet) bool {
	switch p := pk.(type) {
	case *packet.InventoryTransaction:
		if u, ok := p.TransactionData.(*protocol.UseItemTransactionData); ok && u.BlockPosition == f.pos {
			f.queue = append(f.queue, pk)
			return true
		}
	case *packet.ItemStackRequest:
		if f.open {
			for _, req := range p.Requests {
				if f.furnaceAction(req.Actions[0]) {
					f.queue = append(f.queue, pk)
					return true
				}
			}
		}
	case *packet.ContainerClose:
		if p.WindowID == f.window {
			f.queue = append(f.queue, pk)
		}
	}
	return false
}

func isFurnaceContainer(id byte) bool {
	return id == protocol.ContainerFurnaceIngredient || id == protocol.ContainerFurnaceFuel || id == protocol.ContainerFurnaceResult
}

func (f *furnaceSim) furnaceAction(a protocol.StackRequestAction) bool {
	switch v := a.(type) {
	case *protocol.PlaceStackRequestAction:
		return isFurnaceContainer(v.Destination.Container.ContainerID)
	case *protocol.TakeStackRequestAction:
		return isFurnaceContainer(v.Source.Container.ContainerID)
	}
	return false
}

func (f *furnaceSim) tick() {
	queue := f.queue
	f.queue = nil
	for _, pk := range queue {
		switch p := pk.(type) {
		case *packet.InventoryTransaction:
			f.click()
		case *packet.ItemStackRequest:
			for _, req := range p.Requests {
				f.request(req)
			}
		case *packet.ContainerClose:
			f.open = false
		}
	}
	f.cook()
}

func (f *furnaceSim) click() {
	if f.clicks++; f.clicks <= f.ignoreOpens {
		return
	}
	f.opens++
	f.window++
	f.open = true
	f.r.send(&packet.ContainerOpen{WindowID: f.window, ContainerType: 2, ContainerPosition: f.pos})
	content := make([]protocol.ItemInstance, 3)
	for i, s := range f.slots {
		content[i] = instance(s)
	}
	f.r.send(&packet.InventoryContent{WindowID: uint32(f.window), Content: content})
}

func (f *furnaceSim) reply(id int32, status uint8, infos ...protocol.StackResponseContainerInfo) {
	f.r.send(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{Status: status, RequestID: id, ContainerInfo: infos}}})
}

func (f *furnaceSim) slotOf(container byte) int {
	switch container {
	case protocol.ContainerFurnaceIngredient:
		return 0
	case protocol.ContainerFurnaceFuel:
		return 1
	}
	return 2
}

func (f *furnaceSim) request(req protocol.ItemStackRequest) {
	if f.refuse != 0 {
		f.reply(req.RequestID, f.refuse)
		return
	}
	switch v := req.Actions[0].(type) {
	case *protocol.PlaceStackRequestAction:
		dstIdx := f.slotOf(v.Destination.Container.ContainerID)
		src, dst := &f.r.inv[v.Source.Slot], &f.slots[dstIdx]
		n := int(v.Count)
		switch {
		case dstIdx == 2:
			f.reply(req.RequestID, 1)
			return
		case src.id != v.Source.StackNetworkID || src.count < n:
			f.reply(req.RequestID, 49)
			return
		case dst.count > 0 && dst.item != src.item:
			f.reply(req.RequestID, 1)
			return
		}
		if dst.count == 0 {
			*dst = slotStack{item: src.item, id: src.id}
			if n < src.count {
				f.r.nextID++
				dst.id = f.r.nextID
			}
		}
		dst.count += n
		src.count -= n
		if src.count == 0 {
			*src = slotStack{}
		}
		f.r.send(&packet.InventorySlot{WindowID: uint32(f.window), Slot: uint32(dstIdx), NewItem: instance(*dst)})
		f.reply(req.RequestID, 0, protocol.StackResponseContainerInfo{
			Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			SlotInfo:  []protocol.StackResponseSlotInfo{{Slot: v.Source.Slot, Count: byte(src.count), StackNetworkID: src.id}},
		})
	case *protocol.TakeStackRequestAction:
		srcIdx := f.slotOf(v.Source.Container.ContainerID)
		src, dst := &f.slots[srcIdx], &f.r.inv[v.Destination.Slot]
		n := int(v.Count)
		switch {
		case src.count < n || src.id != v.Source.StackNetworkID:
			f.reply(req.RequestID, 49)
			return
		case dst.count > 0 && (dst.item != src.item || dst.id != v.Destination.StackNetworkID):
			f.reply(req.RequestID, 1)
			return
		}
		moved := slotStack{item: src.item, id: src.id}
		if n < src.count {
			f.r.nextID++
			moved.id = f.r.nextID
		}
		if dst.count == 0 {
			*dst = moved
		}
		dst.count += n
		src.count -= n
		left := *src
		if src.count == 0 {
			*src = slotStack{}
		}
		f.r.send(&packet.InventorySlot{WindowID: uint32(f.window), Slot: uint32(srcIdx), NewItem: instance(*src)})
		f.reply(req.RequestID, 0,
			protocol.StackResponseContainerInfo{
				Container: protocol.FullContainerName{ContainerID: v.Source.Container.ContainerID},
				SlotInfo:  []protocol.StackResponseSlotInfo{{Slot: v.Source.Slot, Count: byte(src.count), StackNetworkID: left.id}},
			},
			protocol.StackResponseContainerInfo{
				Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
				SlotInfo:  []protocol.StackResponseSlotInfo{{Slot: v.Destination.Slot, Count: byte(dst.count), StackNetworkID: dst.id}},
			})
	}
}

// cook advances the fire by a tick.
func (f *furnaceSim) cook() {
	if f.stall {
		return
	}
	out, can := f.recipes[f.slots[0].item]
	can = can && f.slots[0].count > 0 && (f.slots[2].count == 0 || f.slots[2].item == out)
	if f.burn == 0 {
		if !can || f.slots[1].count == 0 {
			f.progress = 0
			return
		}
		f.burn = f.fuels[f.slots[1].item]
		if f.slots[1].count--; f.slots[1].count == 0 {
			f.slots[1] = slotStack{}
		}
		f.sync(1)
	}
	f.burn--
	if !can {
		f.progress = 0
		return
	}
	if f.progress++; f.progress < cookTicks {
		return
	}
	f.progress = 0
	if f.slots[2].count == 0 {
		f.r.nextID++
		f.slots[2] = slotStack{item: out, id: f.r.nextID}
	}
	f.slots[2].count++
	if f.slots[0].count--; f.slots[0].count == 0 {
		f.slots[0] = slotStack{}
	}
	f.cooked++
	f.sync(0)
	f.sync(2)
}

// sync tells an open window about a slot that changed.
func (f *furnaceSim) sync(i int) {
	if f.open {
		f.r.send(&packet.InventorySlot{WindowID: uint32(f.window), Slot: uint32(i), NewItem: instance(f.slots[i])})
	}
}

func smeltWorld() simWorld {
	w := simWorld{}
	w.platform(0, 30, 0, 30)
	return w
}

func smeltRig(t *testing.T) (*rig, simWorld, *furnaceSim) {
	t.Helper()
	w := smeltWorld()
	r := newRig(t, w, feetAt(8, 1, 10))
	return r, w, newFurnaceSim(r)
}

func newSmelt(t *testing.T, w simWorld, r *rig, input string, qty int, log *capture) Skill {
	t.Helper()
	var opts []Option
	if log != nil {
		opts = append(opts, WithLogger(log))
	}
	s, err := NewSmelt(w, r.self, furnacePos, input, qty, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSmeltLoadsWaitsAndCollects(t *testing.T) {
	r, w, f := smeltRig(t)
	r.give(0, idRawIron, 8)
	r.give(1, idCoal, 4)
	log := &capture{}
	s := newSmelt(t, w, r, "raw_iron", 8, log)
	if err := s.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(s, 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("iron_ingot"); got != 8 {
		t.Errorf("iron ingots = %d, want 8", got)
	}
	if got := r.have("raw_iron"); got != 0 {
		t.Errorf("raw iron left = %d", got)
	}
	// Eight items need 16 half-smelts: one coal.
	if got := r.have("coal"); got != 3 {
		t.Errorf("coal left = %d, want 3 (one burnt)", got)
	}
	if p := s.(Progresser).Progress(); p != 8 {
		t.Errorf("progress = %d, want 8", p)
	}
	if f.opens != 2 {
		t.Errorf("the furnace was opened %d times, want once to load and once to collect", f.opens)
	}
	if open, _, _ := r.Container(); open {
		t.Error("the furnace window was left open")
	}
}

func TestSmeltPlacesTheFurnaceFirst(t *testing.T) {
	r, w, f := smeltRig(t)
	delete(w, furnacePos)
	w[furnacePos] = "air"
	f.pos = furnacePos
	r.give(0, idRawIron, 2)
	r.give(1, idCoal, 1)
	r.give(2, idFurnaceItem, 1)
	extraBlocks["furnace"] = idFurnaceItem
	defer delete(extraBlocks, "furnace")
	// The sim only opens once the furnace stands; the placed block is "furnace".
	s := newSmelt(t, w, r, "raw_iron", 2, nil)
	st, err := r.run(s, 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w[furnacePos] != "furnace" {
		t.Errorf("the furnace cell is %q", w[furnacePos])
	}
	if r.have("furnace") != 0 {
		t.Error("the furnace item was not spent")
	}
	if got := r.have("iron_ingot"); got != 2 {
		t.Errorf("iron ingots = %d, want 2", got)
	}
}

func TestSmeltReadyNamesWhatIsMissing(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(r *rig)
		want string
	}{
		{"no ore", func(r *rig) { r.give(1, idCoal, 4) }, "no raw_iron in inventory"},
		{"too little ore", func(r *rig) { r.give(0, idRawIron, 3); r.give(1, idCoal, 4) }, "no raw_iron in inventory"},
		{"no fuel", func(r *rig) { r.give(0, idRawIron, 4) }, "no fuel in inventory"},
		{"not enough fuel", func(r *rig) { r.give(0, idRawIron, 4); r.give(1, idOakPlanks, 2) }, "no fuel in inventory"},
	} {
		r, w, _ := smeltRig(t)
		c.set(r)
		err := newSmelt(t, w, r, "raw_iron", 4, nil).Ready(r.self())
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	r, w, f := smeltRig(t)
	delete(w, f.pos)
	r.give(0, idRawIron, 4)
	r.give(1, idCoal, 4)
	if err := newSmelt(t, w, r, "raw_iron", 4, nil).Ready(r.self()); err == nil || err.Error() != "no furnace in inventory" {
		t.Errorf("no furnace: err = %v", err)
	}
}

func TestNewSmeltRefusesWhatAFurnaceDoesNotMake(t *testing.T) {
	r, w, _ := smeltRig(t)
	if _, err := NewSmelt(w, r.self, furnacePos, "diamond", 1); err == nil {
		t.Error("smelting a diamond was accepted")
	}
	for _, q := range []int{0, -1, 65} {
		if _, err := NewSmelt(w, r.self, furnacePos, "raw_iron", q); err == nil {
			t.Errorf("qty %d was accepted", q)
		}
	}
}

func TestSmeltBurnsWoodWhenThereIsNoCoal(t *testing.T) {
	r, w, _ := smeltRig(t)
	r.give(0, idRawIron, 3)
	r.give(1, idOakPlanks, 8)
	st, err := r.run(newSmelt(t, w, r, "raw_iron", 3, nil), 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	// Three items are six half-smelts: two planks.
	if got := r.have("oak_planks"); got != 6 {
		t.Errorf("planks left = %d, want 6", got)
	}
}

func TestSmeltClicksAgainWhenTheFirstClickIsSwallowed(t *testing.T) {
	r, w, f := smeltRig(t)
	f.ignoreOpens = 1
	r.give(0, idRawIron, 1)
	r.give(1, idCoal, 1)
	st, err := r.run(newSmelt(t, w, r, "raw_iron", 1, nil), 6000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.have("iron_ingot") != 1 {
		t.Error("no ingot")
	}
}

func TestSmeltFailsWhenTheFurnaceNeverMakesAnything(t *testing.T) {
	r, w, f := smeltRig(t)
	f.stall = true
	r.give(0, idRawIron, 1)
	r.give(1, idCoal, 1)
	st, err := r.run(newSmelt(t, w, r, "raw_iron", 1, nil), 6000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "made no iron_ingot") {
		t.Fatalf("status %v, err %v, want a failure naming the missing ingot", st, err)
	}
	if open, _, _ := r.Container(); open {
		t.Error("the window was left open on failure")
	}
}

func TestSmeltFailsWhenTheServerRefusesTheLoad(t *testing.T) {
	r, w, f := smeltRig(t)
	f.refuse = 57
	r.give(0, idRawIron, 1)
	r.give(1, idCoal, 1)
	st, err := r.run(newSmelt(t, w, r, "raw_iron", 1, nil), 2000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "status 57") {
		t.Fatalf("status %v, err %v", st, err)
	}
	if open, _, _ := r.Container(); open {
		t.Error("the window was left open on failure")
	}
}

func TestSmeltFailsOnAFurnaceHoldingSomethingElse(t *testing.T) {
	r, w, f := smeltRig(t)
	r.nextID++
	f.slots[0] = slotStack{item: idCobblestone, count: 4, id: r.nextID}
	r.give(0, idRawIron, 1)
	r.give(1, idCoal, 1)
	st, err := r.run(newSmelt(t, w, r, "raw_iron", 1, nil), 2000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "holds something else") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestSmeltResumesAfterAFightWithoutLoadingTwice(t *testing.T) {
	r, w, f := smeltRig(t)
	r.give(0, idRawIron, 4)
	r.give(1, idCoal, 2)
	s := newSmelt(t, w, r, "raw_iron", 4, nil)
	// Run until the furnace is loaded and the unit is waiting.
	for i := 0; f.slots[0].count == 0 || f.open; i++ {
		if i > 2000 {
			t.Fatal("never loaded")
		}
		if st, err := s.Step(t.Context(), r); st != Running {
			t.Fatalf("status %v, err %v", st, err)
		}
	}
	for i := 0; s.(*Smelt).phase != smCook; i++ {
		if i > 200 {
			t.Fatal("never started cooking")
		}
		s.Step(t.Context(), r)
	}
	s.(Stopper).Stop(r)
	s.(Resumer).Resume()
	st, err := r.run(s, 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("iron_ingot"); got != 4 {
		t.Errorf("iron ingots = %d, want 4", got)
	}
	if got := r.have("coal"); got != 1 {
		t.Errorf("coal left = %d, want 1: one burnt, none loaded twice", got)
	}
}

func TestFuelForPicksTheCheapestSufficientFuel(t *testing.T) {
	inv := func(items ...[2]any) belief.Snapshot {
		var s belief.Snapshot
		for i, it := range items {
			s.Inventory = append(s.Inventory, belief.Item{Slot: i, Name: it[0].(string), Count: it[1].(int)})
		}
		return s
	}
	cases := []struct {
		name     string
		snap     belief.Snapshot
		qty      int
		wantItem string
		wantN    int
		wantOK   bool
	}{
		{"charcoal before coal", inv([2]any{"coal", 8}, [2]any{"charcoal", 8}), 8, "charcoal", 1, true},
		{"coal before wood", inv([2]any{"oak_planks", 64}, [2]any{"coal", 3}), 8, "coal", 1, true},
		{"eight items, one coal", inv([2]any{"coal", 5}), 8, "coal", 1, true},
		{"nine items, two coal", inv([2]any{"coal", 5}), 9, "coal", 2, true},
		{"a stack too small is passed over", inv([2]any{"coal", 1}, [2]any{"oak_planks", 12}), 16, "oak_planks", 11, true},
		{"planks are one and a half smelts", inv([2]any{"oak_planks", 64}), 3, "oak_planks", 2, true},
		{"the input is never burnt", inv([2]any{"oak_log", 64}), 4, "", 0, false},
		{"a tool is no fuel", inv([2]any{"wooden_pickaxe", 1}), 1, "", 0, false},
		{"nothing at all", inv(), 1, "", 0, false},
	}
	for _, c := range cases {
		input := "raw_iron"
		if c.name == "the input is never burnt" {
			input = "oak_log"
		}
		item, n, ok := FuelFor(c.snap, input, c.qty)
		if item != c.wantItem || n != c.wantN || ok != c.wantOK {
			t.Errorf("%s: got %q x%d ok=%v, want %q x%d ok=%v", c.name, item, n, ok, c.wantItem, c.wantN, c.wantOK)
		}
	}
}

func TestSmeltOutputsAreClosedAndSorted(t *testing.T) {
	if out, ok := SmeltOutput("raw_iron"); !ok || out != "iron_ingot" {
		t.Errorf("raw_iron -> %q %v", out, ok)
	}
	if _, ok := SmeltOutput("diamond"); ok {
		t.Error("diamond smelts")
	}
	in := SmeltInputs()
	for i := 1; i < len(in); i++ {
		if in[i-1] >= in[i] {
			t.Errorf("inputs not sorted at %d: %v", i, in)
		}
	}
}
