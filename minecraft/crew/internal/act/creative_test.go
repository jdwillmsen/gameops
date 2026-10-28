package act

import (
	"errors"
	"github.com/go-gl/mathgl/mgl32"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	creativeStone    int32  = 1
	creativeStoneCID uint32 = 604
	creativeOakStair int32  = 2
	creativeStairCID uint32 = 77
	creativeWindow   byte   = 3
	// takenStackID is the small per-session ID the server gives a taken stack.
	takenStackID int32 = 4
	// creativeStack is the size the first take asks for.
	creativeStack = 64
)

// creativeContent is the table as the server sends it at login: a variant of
// the stone item first, so the default entry is the one that counts.
func creativeContent() *packet.CreativeContent {
	item := func(cid uint32, id int32) protocol.CreativeItem {
		return protocol.CreativeItem{CreativeItemNetworkID: cid, Item: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: 1}}
	}
	return &packet.CreativeContent{Items: []protocol.CreativeItem{
		item(creativeStoneCID, creativeStone),
		item(creativeStairCID, creativeOakStair),
		item(999, creativeStone),
	}}
}

func newCreativeAct(creative bool) (*Actuator, *sink) {
	a, s := newAct(flat(), Pose{})
	a.ItemNames([]protocol.ItemEntry{
		{Name: "minecraft:stone", RuntimeID: int16(creativeStone)},
		{Name: "minecraft:oak_stairs", RuntimeID: int16(creativeOakStair)},
		{Name: "minecraft:dirt", RuntimeID: 9},
	})
	a.SetCreative(func() bool { return creative })
	a.Apply(creativeContent())
	return a, s
}

// creativeServer answers as the lab's server does: the inventory screen opens
// on request, and a creative request is judged by the screen, the mode and
// the source it names.
type creativeServer struct {
	a        *Actuator
	s        *sink
	creative bool
	stop     chan struct{}
	done     sync.WaitGroup
	// silent withholds every answer.
	silent atomic.Bool
	// screen is whether the inventory screen is open on the server.
	screen bool
	// onOpen runs as the screen opens, to change the inventory between a
	// take's plan and its request.
	onOpen func()
	// taken counts the accepted takes, so each stack gets its own ID.
	taken int32
	// stacksTo, when set, is the most the item stacks to; a bigger take is
	// refused.
	stacksTo byte
}

func serveCreative(a *Actuator, s *sink, creative bool) *creativeServer {
	cs := &creativeServer{a: a, s: s, creative: creative, stop: make(chan struct{})}
	cs.done.Add(1)
	go cs.serve()
	return cs
}

func (cs *creativeServer) close() {
	close(cs.stop)
	cs.done.Wait()
}

func (cs *creativeServer) serve() {
	defer cs.done.Done()
	seen := 0
	for {
		select {
		case <-cs.stop:
			return
		default:
		}
		cs.s.mu.Lock()
		pending := append([]packet.Packet(nil), cs.s.got[seen:]...)
		seen = len(cs.s.got)
		cs.s.mu.Unlock()
		for _, pk := range pending {
			switch p := pk.(type) {
			case *packet.Interact:
				cs.screen = true
				if cs.onOpen != nil {
					cs.onOpen()
				}
				cs.a.Apply(&packet.ContainerOpen{WindowID: creativeWindow, ContainerType: 0xff, ContainerEntityUniqueID: -1})
			case *packet.ContainerClose:
				cs.screen = false
				cs.a.Apply(&packet.ContainerClose{WindowID: p.WindowID, ContainerType: 247})
			case *packet.ItemStackRequest:
				if !cs.silent.Load() {
					cs.a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{cs.judge(p.Requests[0])}})
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
}

func (cs *creativeServer) judge(r protocol.ItemStackRequest) protocol.ItemStackResponse {
	resp := protocol.ItemStackResponse{RequestID: r.RequestID}
	take := r.Actions[len(r.Actions)-1].(*protocol.TakeStackRequestAction)
	var destroy *protocol.DestroyStackRequestAction
	if d, ok := r.Actions[0].(*protocol.DestroyStackRequestAction); ok {
		destroy = d
	}
	slot := cs.a.inv[take.Destination.Slot]
	switch {
	case !cs.screen:
		resp.Status = StatusInvalidCraft
	case !cs.creative:
		resp.Status = StatusNotCreative
	case cs.stacksTo != 0 && take.Count > cs.stacksTo:
		resp.Status = StatusBadAmount
	case take.Source.StackNetworkID != r.RequestID || take.Source.Slot != 50:
		resp.Status = StatusBadSource
	case slot.Stack.Count > 0 && (destroy == nil || destroy.Source.Slot != take.Destination.Slot || destroy.Source.StackNetworkID != slot.StackNetworkID):
		// The lab refuses a take onto an occupied slot, whatever stack ID the
		// destination quotes, unless the same request destroys that stack.
		resp.Status = StatusOccupied
	default:
		cs.taken++
		resp.ContainerInfo = []protocol.StackResponseContainerInfo{{
			Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			SlotInfo:  []protocol.StackResponseSlotInfo{{Slot: take.Destination.Slot, HotbarSlot: take.Destination.Slot, Count: take.Count, StackNetworkID: takenStackID + cs.taken}},
		}}
	}
	return resp
}

func TestTakeCreativeAsksForTheNamedItemIntoAnEmptyHotbarSlot(t *testing.T) {
	a, s := newCreativeAct(true)
	a.Apply(&packet.InventorySlot{WindowID: 0, Slot: 0, NewItem: cobbleInstance(5, 8)})
	srv := serveCreative(a, s, true)
	defer srv.close()

	if err := a.TakeCreative("stone"); err != nil {
		t.Fatal(err)
	}
	reqs := stackRequests(s)
	if len(reqs) != 1 || len(reqs[0].Requests) != 1 {
		t.Fatalf("sent %d item requests, want one request", len(reqs))
	}
	r := reqs[0].Requests[0]
	if len(r.Actions) != 2 {
		t.Fatalf("actions = %+v", r.Actions)
	}
	cc, ok := r.Actions[0].(*protocol.CraftCreativeStackRequestAction)
	if !ok || cc.CreativeItemNetworkID != creativeStoneCID || cc.NumberOfCrafts != 1 {
		t.Errorf("first action = %+v, want the creative ID of the item's default entry", r.Actions[0])
	}
	take, ok := r.Actions[1].(*protocol.TakeStackRequestAction)
	if !ok {
		t.Fatalf("second action is %T", r.Actions[1])
	}
	if take.Source.Container.ContainerID != protocol.ContainerCreatedOutput || take.Source.Slot != 50 || take.Source.StackNetworkID != r.RequestID {
		t.Errorf("source = %+v, want created output slot 50 quoting the request's own ID %d", take.Source, r.RequestID)
	}
	if take.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory || take.Destination.Slot != 1 || take.Destination.StackNetworkID != 0 {
		t.Errorf("destination = %+v, want the first empty hotbar slot (1)", take.Destination)
	}
	if got := a.inv[1]; got.Stack.NetworkID != creativeStone || got.Stack.Count != uint16(creativeStacks[0]) || got.StackNetworkID != takenStackID+1 {
		t.Errorf("inventory slot 1 = %+v, want the taken stack", got)
	}
}

func TestTakeCreativeOpensTheScreenFirstAndClosesItAfter(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	defer srv.close()
	if err := a.TakeCreative("stone"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var kinds []string
	for _, pk := range s.got {
		switch p := pk.(type) {
		case *packet.Interact:
			if p.ActionType != packet.InteractActionOpenInventory || p.TargetEntityRuntimeID != 7 {
				t.Errorf("interact = %+v", p)
			}
			kinds = append(kinds, "open")
		case *packet.ItemStackRequest:
			kinds = append(kinds, "request")
		case *packet.ContainerClose:
			if p.WindowID != creativeWindow || p.ContainerType != closeContainerType {
				t.Errorf("close = %+v, want the window the server opened", p)
			}
			kinds = append(kinds, "close")
		}
	}
	if len(kinds) != 3 || kinds[0] != "open" || kinds[1] != "request" || kinds[2] != "close" {
		t.Errorf("sequence = %v", kinds)
	}
}

func TestTakeCreativeNamesEachFailure(t *testing.T) {
	t.Run("survival", func(t *testing.T) {
		a, s := newCreativeAct(false)
		if err := a.TakeCreative("stone"); !errors.Is(err, ErrNotCreative) {
			t.Fatalf("err = %v, want ErrNotCreative", err)
		}
		if len(s.got) != 0 {
			t.Errorf("sent %d packets from a unit known not to be creative", len(s.got))
		}
	})
	t.Run("server says not creative", func(t *testing.T) {
		a, s := newCreativeAct(true)
		srv := serveCreative(a, s, false)
		defer srv.close()
		if err := a.TakeCreative("stone"); !errors.Is(err, ErrNotCreative) {
			t.Fatalf("err = %v, want ErrNotCreative", err)
		}
	})
	t.Run("item without an entry", func(t *testing.T) {
		a, _ := newCreativeAct(true)
		if err := a.TakeCreative("dirt"); !errors.Is(err, ErrNoCreativeItem) {
			t.Fatalf("err = %v, want ErrNoCreativeItem", err)
		}
		if err := a.TakeCreative("bedrock"); !errors.Is(err, ErrNoCreativeItem) {
			t.Fatalf("err = %v, want ErrNoCreativeItem for a name outside the item table", err)
		}
	})
	t.Run("hotbar full of items the unit did not take", func(t *testing.T) {
		a, s := newCreativeAct(true)
		for i := uint32(0); i < 9; i++ {
			a.Apply(&packet.InventorySlot{WindowID: 0, Slot: i, NewItem: cobbleInstance(1, int32(i)+1)})
		}
		if err := a.TakeCreative("stone"); !errors.Is(err, ErrHotbarFull) {
			t.Fatalf("err = %v, want ErrHotbarFull", err)
		}
		if len(s.got) != 0 {
			t.Errorf("sent %d packets for a take that had nowhere to land", len(s.got))
		}
	})
	for name, c := range map[string]struct {
		status uint8
		want   error
	}{
		"bad source":      {StatusBadSource, ErrBadSource},
		"screen not open": {StatusInvalidCraft, ErrScreenNotOpen},
		"other refusal":   {StatusOccupied, ErrRejected},
	} {
		t.Run(name, func(t *testing.T) {
			a, s := newCreativeAct(true)
			a.Apply(&packet.ContainerOpen{WindowID: creativeWindow, ContainerType: 0xff})
			go func() {
				for len(stackRequests(s)) == 0 {
					time.Sleep(time.Millisecond)
				}
				r := stackRequests(s)[0].Requests[0]
				a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{RequestID: r.RequestID, Status: c.status}}})
			}()
			if err := a.TakeCreative("stone"); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestTakeCreativeGivesUpOnASilentServer(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	srv.silent.Store(true)
	defer srv.close()
	start := time.Now()
	err := a.TakeCreative("stone")
	if err == nil || errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if time.Since(start) > creativeTakeDeadline {
		t.Errorf("waited %v", time.Since(start))
	}
}

func TestCreativeTableIsTakenFromTheLoginPacket(t *testing.T) {
	a, _ := newCreativeAct(true)
	if got := a.creativeIDs[creativeOakStair]; got != creativeStairCID {
		t.Errorf("stairs = %d, want %d", got, creativeStairCID)
	}
	if got := a.creativeIDs[creativeStone]; got != creativeStoneCID {
		t.Errorf("stone = %d, want the first entry %d", got, creativeStoneCID)
	}
}

func TestPlaceBlockTakesTheItemFirstWhenCreative(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	a, s := newCreativeAct(true)
	a.solid = stoneBelow(target)
	a.pose = Pose{Feet: mgl32.Vec3{1.5, 1, 2.5}, OnGround: true}
	srv := serveCreative(a, s, true)
	defer srv.close()

	if err := a.PlaceBlock(target, "stone"); err != nil {
		t.Fatal(err)
	}
	if got := len(stackRequests(s)); got != 1 {
		t.Fatalf("sent %d item requests, want one take", got)
	}
	s.mu.Lock()
	last := s.got[len(s.got)-1]
	s.mu.Unlock()
	tx, ok := last.(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("last packet is %T, want the placement click", last)
	}
	if u := tx.TransactionData.(*protocol.UseItemTransactionData); u.HotBarSlot != 0 || u.HeldItem.Stack.NetworkID != creativeStone {
		t.Errorf("click = %+v, want the taken stone in hotbar slot 0", u)
	}

	if err := a.PlaceBlock(target, "stone"); err != nil {
		t.Fatal(err)
	}
	if got := len(stackRequests(s)); got != 1 {
		t.Errorf("a second placement took again: %d requests", got)
	}
}

func TestPlaceBlockInSurvivalNeverTakes(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	a, s := newCreativeAct(false)
	a.solid = stoneBelow(target)
	a.pose = Pose{Feet: mgl32.Vec3{1.5, 1, 2.5}, OnGround: true}
	if err := a.PlaceBlock(target, "stone"); !errors.Is(err, ErrNoItem) {
		t.Fatalf("err = %v, want ErrNoItem", err)
	}
	if len(s.got) != 0 {
		t.Errorf("sent %d packets", len(s.got))
	}
}

func TestPlaceBlockFailsClearlyForAnItemWithoutACreativeEntry(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	a, _ := newCreativeAct(true)
	a.solid = stoneBelow(target)
	a.pose = Pose{Feet: mgl32.Vec3{1.5, 1, 2.5}, OnGround: true}
	if err := a.PlaceBlock(target, "dirt"); !errors.Is(err, ErrNoCreativeItem) {
		t.Fatalf("err = %v, want ErrNoCreativeItem", err)
	}
}

// fillHotbar takes stone and stairs alternately into the nine slots, stone in
// the even ones.
func fillHotbar(t *testing.T, a *Actuator) {
	t.Helper()
	for i := 0; i < hotbarSlots; i++ {
		item := "stone"
		if i%2 == 1 {
			item = "oak_stairs"
		}
		if err := a.TakeCreative(item); err != nil {
			t.Fatalf("take %d (%s): %v", i, item, err)
		}
	}
}

func destroyOf(t *testing.T, r protocol.ItemStackRequest) *protocol.DestroyStackRequestAction {
	t.Helper()
	if len(r.Actions) != 3 {
		t.Fatalf("actions = %+v, want destroy, craft, take", r.Actions)
	}
	d, ok := r.Actions[0].(*protocol.DestroyStackRequestAction)
	if !ok {
		t.Fatalf("first action is %T, want a destroy of the slot being replaced", r.Actions[0])
	}
	return d
}

func TestTakeCreativeReplacesTheLeastRecentlyUsedTakenSlotOnAFullHotbar(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	defer srv.close()
	fillHotbar(t, a)

	// Slot 0 is the oldest take, but the unit just used it.
	a.mu.Lock()
	_, _, ok := a.hotbarHolding(creativeStone)
	a.holdItem("stone")
	a.mu.Unlock()
	if !ok {
		t.Fatal("stone is not on the hotbar")
	}
	before := len(stackRequests(s))
	if err := a.TakeCreative("oak_stairs"); err != nil {
		t.Fatalf("take on a full hotbar: %v", err)
	}
	reqs := stackRequests(s)
	if len(reqs) != before+1 {
		t.Fatalf("sent %d requests, want one more", len(reqs)-before)
	}
	r := reqs[len(reqs)-1].Requests[0]
	d := destroyOf(t, r)
	if d.Source.Slot != 2 || d.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory {
		t.Errorf("destroyed %+v, want hotbar slot 2: slot 0 was used last and stairs are the item asked for", d.Source)
	}
	if d.Count != creativeStack || d.Source.StackNetworkID == 0 {
		t.Errorf("destroy = %+v, want the whole stack quoting its stack ID", d)
	}
	take := r.Actions[2].(*protocol.TakeStackRequestAction)
	if take.Destination.Slot != 2 || take.Destination.StackNetworkID != 0 {
		t.Errorf("destination = %+v, want the freed slot 2", take.Destination)
	}
	if got := a.inv[2]; got.Stack.NetworkID != creativeOakStair || got.Stack.Count != creativeStack || got.StackNetworkID != takenStackID+10 {
		t.Errorf("slot 2 = %+v, want the new stairs stack", got)
	}
	// Slot 2 now holds stairs, so the next stone must replace the oldest
	// remaining stairs slot and never the stone being asked for.
	if err := a.TakeCreative("stone"); err != nil {
		t.Fatal(err)
	}
	reqs = stackRequests(s)
	if d := destroyOf(t, reqs[len(reqs)-1].Requests[0]); d.Source.Slot != 1 {
		t.Errorf("destroyed slot %d, want 1, the oldest stairs slot", d.Source.Slot)
	}
}

func TestTakeCreativeNeverReplacesAnItemItDidNotTake(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	defer srv.close()
	for i := uint32(0); i < 8; i++ {
		a.Apply(&packet.InventorySlot{WindowID: 0, Slot: i, NewItem: cobbleInstance(1, int32(i)+1)})
	}
	if err := a.TakeCreative("stone"); err != nil {
		t.Fatal(err)
	}
	// The hotbar is full: eight foreign stacks and one taken stone, which is
	// the item asked for.
	if err := a.TakeCreative("stone"); !errors.Is(err, ErrHotbarFull) {
		t.Fatalf("err = %v, want ErrHotbarFull: the only taken stack is the requested item", err)
	}
	if err := a.TakeCreative("oak_stairs"); err != nil {
		t.Fatalf("stairs onto the taken stone slot: %v", err)
	}
	if got := a.inv[8]; got.Stack.NetworkID != creativeOakStair {
		t.Errorf("slot 8 = %+v, want the stairs", got)
	}
	if got := a.inv[0]; got.Stack.NetworkID == creativeOakStair {
		t.Error("a foreign stack was replaced")
	}
}

func TestTakeCreativeForgetsASlotThatNoLongerHoldsWhatItTook(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	defer srv.close()
	fillHotbar(t, a)
	for i := uint32(0); i < 9; i++ {
		a.Apply(&packet.InventorySlot{WindowID: 0, Slot: i, NewItem: cobbleInstance(1, int32(i)+50)})
	}
	if err := a.TakeCreative("oak_stairs"); !errors.Is(err, ErrHotbarFull) {
		t.Fatalf("err = %v, want ErrHotbarFull once the server replaced every taken stack", err)
	}
}

func TestPlaceBlockTakesCreativelyWhenTheItemOnlySitsInTheBackpack(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	a, s := newCreativeAct(true)
	a.solid = stoneBelow(target)
	a.pose = Pose{Feet: mgl32.Vec3{1.5, 1, 2.5}, OnGround: true}
	srv := serveCreative(a, s, true)
	defer srv.close()
	for i := 0; i < hotbarSlots; i++ {
		if err := a.TakeCreative("stone"); err != nil {
			t.Fatal(err)
		}
	}
	stairs := protocol.ItemInstance{StackNetworkID: 90, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: creativeOakStair}, Count: 5}}
	a.Apply(&packet.InventorySlot{WindowID: 0, Slot: 20, NewItem: stairs})

	if err := a.PlaceBlock(target, "oak_stairs"); err != nil {
		t.Fatalf("place with the stairs only in the backpack: %v", err)
	}
	s.mu.Lock()
	last := s.got[len(s.got)-1]
	s.mu.Unlock()
	tx, ok := last.(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("last packet is %T, want the placement click", last)
	}
	if u := tx.TransactionData.(*protocol.UseItemTransactionData); u.HeldItem.Stack.NetworkID != creativeOakStair {
		t.Errorf("click held %+v, want the stairs", u.HeldItem)
	}
}
func TestTakeCreativeRechecksTheSlotAfterTheScreenOpens(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	defer srv.close()
	fillHotbar(t, a)
	// The oldest taken stone is slot 0; before the request goes out the
	// server swaps in a stack the unit did not take.
	foreign := cobbleInstance(3, 99)
	srv.onOpen = func() {
		a.Apply(&packet.InventorySlot{WindowID: 0, Slot: 0, NewItem: foreign})
		srv.onOpen = nil
	}
	if err := a.TakeCreative("oak_stairs"); err != nil {
		t.Fatal(err)
	}
	reqs := stackRequests(s)
	d := destroyOf(t, reqs[len(reqs)-1].Requests[0])
	if d.Source.Slot != 2 {
		t.Errorf("destroyed slot %d, want 2: slot 0 no longer holds a stack the unit took", d.Source.Slot)
	}
	if got := a.inv[0]; got.StackNetworkID != 99 {
		t.Errorf("slot 0 = %+v, want the foreign stack untouched", got)
	}
}
func TestALateAnswerToATimedOutTakeIsRecordedAndEvictable(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	defer srv.close()
	fillHotbar(t, a)
	srv.silent.Store(true)
	if err := a.TakeCreative("oak_stairs"); err == nil {
		t.Fatal("take answered by a silent server")
	}
	reqs := stackRequests(s)
	r := reqs[len(reqs)-1].Requests[0]
	d := destroyOf(t, r)
	slot := int(d.Source.Slot)

	const lateStackID = 777
	a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{
		RequestID: r.RequestID,
		ContainerInfo: []protocol.StackResponseContainerInfo{{
			Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			SlotInfo:  []protocol.StackResponseSlotInfo{{Slot: byte(slot), HotbarSlot: byte(slot), Count: creativeStack, StackNetworkID: lateStackID}},
		}},
	}}})

	a.mu.Lock()
	got, taken, used := a.inv[slot], a.takenStack[slot], a.slotUse[slot]
	leaks := len(a.reqItem) + len(a.replacing) + len(a.abandoned) + len(a.takes)
	a.mu.Unlock()
	if got.Stack.NetworkID != creativeOakStair || got.StackNetworkID != lateStackID {
		t.Errorf("slot %d = %+v, want the late stairs stack", slot, got)
	}
	if taken != lateStackID || used == 0 {
		t.Errorf("slot %d recorded as stack %d used at %d, want stack %d and a use time", slot, taken, used, lateStackID)
	}
	if leaks != 0 {
		t.Errorf("%d bookkeeping entries left for an answered request", leaks)
	}

	// Evictable: the next take may replace it, quoting the late stack's ID.
	srv.silent.Store(false)
	for i := 0; i < hotbarSlots; i++ {
		a.mu.Lock()
		a.slotUse[i] = uint64(100 + i)
		a.mu.Unlock()
	}
	a.mu.Lock()
	a.slotUse[slot] = 1
	a.mu.Unlock()
	if err := a.TakeCreative("stone"); err != nil {
		t.Fatal(err)
	}
	reqs = stackRequests(s)
	if d := destroyOf(t, reqs[len(reqs)-1].Requests[0]); int(d.Source.Slot) != slot || d.Source.StackNetworkID != lateStackID {
		t.Errorf("destroy = %+v, want slot %d quoting stack %d", d.Source, slot, lateStackID)
	}
}

func TestAnAbandonedTakeThatNeverGetsAnswerIsForgottenAfterTheHorizon(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	defer srv.close()
	srv.silent.Store(true)
	if err := a.TakeCreative("stone"); err == nil {
		t.Fatal("take answered by a silent server")
	}
	a.mu.Lock()
	for req, tk := range a.takes {
		tk.at = tk.at.Add(-2 * abandonHorizon)
		a.takes[req] = tk
	}
	a.mu.Unlock()
	srv.silent.Store(false)
	if err := a.TakeCreative("stone"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if n := len(a.reqItem) + len(a.abandoned) + len(a.takes) + len(a.replacing); n != 0 {
		t.Errorf("%d entries left for the request nobody answered", n)
	}
}

func TestDeathForgetsWhichSlotsTheUnitTook(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	defer srv.close()
	fillHotbar(t, a)
	a.Died()
	// After the respawn the server hands back a hotbar of the same items,
	// which the unit has no claim on.
	for i := uint32(0); i < hotbarSlots; i++ {
		held := protocol.ItemInstance{StackNetworkID: int32(200 + i), Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: creativeOakStair}, Count: 64}}
		a.Apply(&packet.InventorySlot{WindowID: 0, Slot: i, NewItem: held})
	}
	if err := a.TakeCreative("stone"); !errors.Is(err, ErrHotbarFull) {
		t.Fatalf("err = %v, want ErrHotbarFull: no slot is the unit's own after a death", err)
	}
}

func TestTakeCreativeNeverReplacesAForeignStackOfATakenItemType(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	defer srv.close()
	fillHotbar(t, a)
	// The server hands back the same item types in every slot, but they are
	// other stacks than the ones the unit took.
	for i := uint32(0); i < hotbarSlots; i++ {
		id := creativeStone
		if i%2 == 1 {
			id = creativeOakStair
		}
		stack := protocol.ItemInstance{StackNetworkID: int32(300 + i), Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: 64}}
		a.Apply(&packet.InventorySlot{WindowID: 0, Slot: i, NewItem: stack})
	}
	if err := a.TakeCreative("oak_stairs"); !errors.Is(err, ErrHotbarFull) {
		t.Fatalf("err = %v, want ErrHotbarFull: no stack here is one the unit took", err)
	}
}

func TestTakeCreativeStepsDownTheStackForAnItemThatStacksToSixteen(t *testing.T) {
	a, s := newCreativeAct(true)
	srv := serveCreative(a, s, true)
	srv.stacksTo = 16
	defer srv.close()
	if err := a.TakeCreative("stone"); err != nil {
		t.Fatal(err)
	}
	if got := a.inv[0]; got.Stack.Count != 16 {
		t.Errorf("slot 0 = %+v, want a stack of 16", got)
	}
	var counts []byte
	for _, p := range stackRequests(s) {
		counts = append(counts, p.Requests[0].Actions[1].(*protocol.TakeStackRequestAction).Count)
	}
	if len(counts) != 2 || counts[0] != 64 || counts[1] != 16 {
		t.Errorf("asked for %v, want 64 then 16", counts)
	}
}
