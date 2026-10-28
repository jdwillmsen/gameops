package act

import (
	"errors"
	"github.com/go-gl/mathgl/mgl32"
	"sync"
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
	silent bool
	// screen is whether the inventory screen is open on the server.
	screen bool
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
				cs.a.Apply(&packet.ContainerOpen{WindowID: creativeWindow, ContainerType: 0xff, ContainerEntityUniqueID: -1})
			case *packet.ContainerClose:
				cs.screen = false
				cs.a.Apply(&packet.ContainerClose{WindowID: p.WindowID, ContainerType: 247})
			case *packet.ItemStackRequest:
				if !cs.silent {
					cs.a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{cs.judge(p.Requests[0])}})
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
}

func (cs *creativeServer) judge(r protocol.ItemStackRequest) protocol.ItemStackResponse {
	resp := protocol.ItemStackResponse{RequestID: r.RequestID}
	take := r.Actions[1].(*protocol.TakeStackRequestAction)
	switch {
	case !cs.screen:
		resp.Status = StatusInvalidCraft
	case !cs.creative:
		resp.Status = StatusNotCreative
	case take.Source.StackNetworkID != r.RequestID || take.Source.Slot != 50:
		resp.Status = StatusBadSource
	default:
		resp.ContainerInfo = []protocol.StackResponseContainerInfo{{
			Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			SlotInfo:  []protocol.StackResponseSlotInfo{{Slot: take.Destination.Slot, HotbarSlot: take.Destination.Slot, Count: take.Count, StackNetworkID: takenStackID}},
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
	if got := a.inv[1]; got.Stack.NetworkID != creativeStone || got.Stack.Count != creativeStack || got.StackNetworkID != takenStackID {
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
	t.Run("hotbar full", func(t *testing.T) {
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
	srv.silent = true
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
