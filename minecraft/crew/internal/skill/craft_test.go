package skill

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/craft"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// Item and recipe network IDs of the fake server.
const (
	craftPlanksID     int32  = 401
	craftStickID      int32  = 402
	craftTableID      int32  = 403
	craftHoeID        int32  = 404
	craftPlanksRecipe uint32 = 210
	craftHoeRecipe    uint32 = 213
)

var craftItems = []protocol.ItemEntry{
	{Name: "minecraft:oak_log", RuntimeID: int16(idOakLog)},
	{Name: "minecraft:oak_planks", RuntimeID: int16(craftPlanksID)},
	{Name: "minecraft:stick", RuntimeID: int16(craftStickID)},
	{Name: "minecraft:crafting_table", RuntimeID: int16(craftTableID)},
	{Name: "minecraft:wooden_hoe", RuntimeID: int16(craftHoeID)},
}

// craftServer is a real actuator and belief tracker against a simulated
// server that runs on its own goroutine. The chest rig answers from the
// stepping goroutine, but a craft blocks its own step until the answer
// arrives, so this server answers while the step is blocked: it opens the
// crafting table window when the unit clicks it and answers stack requests
// the way the lab server does.
type craftServer struct {
	mu      sync.Mutex
	got     []packet.Packet
	a       *act.Actuator
	tracker *belief.Tracker
	table   protocol.BlockPos

	stop    chan struct{}
	window  byte
	answers int
	nextID  int32
}

func newCraftRig(t *testing.T, w simWorld, feet mgl32.Vec3, table protocol.BlockPos) (*craftServer, *belief.Tracker) {
	t.Helper()
	tr := belief.New(time.Now)
	tr.Spawn(1, feet.Add(mgl32.Vec3{0, eyeHeight, 0}), 0, 0, 0)
	tr.ItemNames(craftItems)
	s := &craftServer{tracker: tr, table: table, nextID: 900, stop: make(chan struct{})}
	s.a = act.New(s, 1, act.Pose{Feet: feet, OnGround: true}, path.NewSolid(w))
	s.a.ItemNames(craftItems)
	s.a.Crafting(craftRecipes(), func() []belief.Item { return tr.Snapshot().Inventory })
	go s.serve()
	t.Cleanup(s.halt)
	return s, tr
}

// craftRecipes holds the planks recipe the 2x2 grid makes anywhere and the
// hoe recipe a crafting table is needed for.
func craftRecipes() *craft.Book {
	b := craft.NewBook()
	b.Store(&packet.CraftingData{
		ClearRecipes: true,
		ShapelessRecipes: []protocol.ShapelessRecipe{{
			RecipeID: "oak_planks",
			Input: []protocol.ItemDescriptorCount{{
				Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_log"},
				Count:      1,
			}},
			Output: []protocol.ItemStack{{
				ItemType: protocol.ItemType{NetworkID: craftPlanksID}, Count: 4,
			}},
			RecipeNetworkID: craftPlanksRecipe,
		}},
		ShapedRecipes: []protocol.ShapedRecipe{{
			RecipeID: "wooden_hoe", Width: 2, Height: 3,
			Input: []protocol.ItemDescriptorCount{
				{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_planks"}, Count: 1},
				{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_planks"}, Count: 1},
				{Descriptor: &protocol.InvalidItemDescriptor{}, Count: 0},
				{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:stick"}, Count: 1},
				{Descriptor: &protocol.InvalidItemDescriptor{}, Count: 0},
				{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:stick"}, Count: 1},
			},
			Output: []protocol.ItemStack{{
				ItemType: protocol.ItemType{NetworkID: craftHoeID}, Count: 1,
			}},
			Block:           "crafting_table",
			RecipeNetworkID: craftHoeRecipe,
		}},
	}, craftItems)
	return b
}

func (s *craftServer) WritePacket(pk packet.Packet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, pk)
	return nil
}

func (s *craftServer) halt() { close(s.stop) }

// send applies a server packet to belief and the actuator, in the order the
// read loop does.
func (s *craftServer) send(pk packet.Packet) {
	s.tracker.Apply(pk)
	s.a.Apply(pk)
}

func (s *craftServer) serve() {
	seen := 0
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		s.mu.Lock()
		pending := append([]packet.Packet(nil), s.got[seen:]...)
		seen = len(s.got)
		s.mu.Unlock()
		for _, pk := range pending {
			s.react(pk)
		}
		time.Sleep(time.Millisecond)
	}
}

func (s *craftServer) react(pk packet.Packet) {
	switch p := pk.(type) {
	case *packet.InventoryTransaction:
		use, ok := p.TransactionData.(*protocol.UseItemTransactionData)
		if ok && use.ActionType == protocol.UseItemActionClickBlock && use.BlockPosition == s.table {
			s.window++
			s.send(&packet.ContainerOpen{
				WindowID:                s.window,
				ContainerType:           protocol.ContainerTypeWorkbench,
				ContainerPosition:       s.table,
				ContainerEntityUniqueID: -1,
			})
		}
	case *packet.ItemStackRequest:
		for _, req := range p.Requests {
			s.answer(req)
		}
	}
}

// answer replies to a craft request: the consumed stacks lose their count,
// the crafted stack appears in the destination with a fresh ID the tracker
// cannot name, and so stays invisible to belief, exactly as on the server.
func (s *craftServer) answer(req protocol.ItemStackRequest) {
	s.answers++
	var slots []protocol.StackResponseSlotInfo
	for _, action := range req.Actions {
		switch act := action.(type) {
		case *protocol.ConsumeStackRequestAction:
			var ref belief.Item
			for _, it := range s.tracker.Snapshot().Inventory {
				if it.Slot == int(act.Source.Slot) {
					ref = it
				}
			}
			left := 0
			if ref.Count > int(act.Count) {
				left = ref.Count - int(act.Count)
			}
			slots = append(slots, protocol.StackResponseSlotInfo{
				Slot: act.Source.Slot, StackNetworkID: ref.StackNetworkID, Count: byte(left),
			})
		case *protocol.TakeStackRequestAction:
			s.nextID++
			slots = append(slots, protocol.StackResponseSlotInfo{
				Slot: act.Destination.Slot, StackNetworkID: s.nextID, Count: act.Count,
			})
		}
	}
	s.send(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{
		RequestID: req.RequestID,
		ContainerInfo: []protocol.StackResponseContainerInfo{{
			Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			SlotInfo:  slots,
		}},
	}}})
}

func (s *craftServer) requests() []protocol.ItemStackRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []protocol.ItemStackRequest
	for _, pk := range s.got {
		if r, ok := pk.(*packet.ItemStackRequest); ok {
			out = append(out, r.Requests...)
		}
	}
	return out
}

func TestCraftMakesPlanksUntilTheCount(t *testing.T) {
	w := simWorld{}
	w.platform(-4, 4, -4, 4)
	s, tr := newCraftRig(t, w, mgl32.Vec3{0.5, 0, 0.5}, protocol.BlockPos{})
	tr.Apply(&packet.InventorySlot{WindowID: 0, Slot: 3, NewItem: protocol.ItemInstance{
		StackNetworkID: 41,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: idOakLog}, Count: 12},
	}})

	sk := NewCraft(w, tr.Snapshot, "oak_planks", 8)
	st := runCraft(t, sk, s.a)
	if st != Done {
		t.Fatalf("status = %v, want Done", st)
	}
	if got := sk.(Progresser).Progress(); got != 8 {
		t.Errorf("progress = %d, want 8 planks", got)
	}
	reqs := s.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d craft requests, want 2", len(reqs))
	}
	for _, r := range reqs {
		cr, ok := r.Actions[0].(*protocol.CraftRecipeStackRequestAction)
		if !ok || cr.RecipeNetworkID != craftPlanksRecipe {
			t.Errorf("craft action = %+v", r.Actions[0])
		}
	}
	// Both crafts paid with the same log stack, which the answers shrank.
	if have := haveItems(tr, "oak_log"); have != 10 {
		t.Errorf("logs left = %d, want 10", have)
	}
}

func TestCraftUsesACraftingTableWithinReach(t *testing.T) {
	w := simWorld{}
	w.platform(-4, 4, -4, 4)
	table := protocol.BlockPos{1, 0, 2}
	w[table] = "crafting_table"
	s, tr := newCraftRig(t, w, mgl32.Vec3{0.5, 0, 0.5}, table)
	tr.Apply(&packet.InventorySlot{WindowID: 0, Slot: 1, NewItem: protocol.ItemInstance{
		StackNetworkID: 42,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: craftPlanksID}, Count: 5},
	}})
	tr.Apply(&packet.InventorySlot{WindowID: 0, Slot: 2, NewItem: protocol.ItemInstance{
		StackNetworkID: 43,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: craftStickID}, Count: 3},
	}})

	sk := NewCraft(w, tr.Snapshot, "wooden_hoe", 1)
	st := runCraft(t, sk, s.a)
	if st != Done {
		t.Fatalf("status = %v, want Done", st)
	}
	if got := sk.(Progresser).Progress(); got != 1 {
		t.Errorf("progress = %d, want one hoe", got)
	}
	reqs := s.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d craft requests, want 1", len(reqs))
	}
	if cr, ok := reqs[0].Actions[0].(*protocol.CraftRecipeStackRequestAction); !ok || cr.RecipeNetworkID != craftHoeRecipe {
		t.Errorf("craft action = %+v", reqs[0].Actions[0])
	}
	// The skill clicked the table and closed the window it opened.
	clicked, closed := false, false
	s.mu.Lock()
	for _, pk := range s.got {
		switch p := pk.(type) {
		case *packet.InventoryTransaction:
			if use, ok := p.TransactionData.(*protocol.UseItemTransactionData); ok && use.BlockPosition == table {
				clicked = true
			}
		case *packet.ContainerClose:
			closed = true
		}
	}
	s.mu.Unlock()
	if !clicked || !closed {
		t.Errorf("clicked = %v closed = %v, want the table clicked and shut again", clicked, closed)
	}
}

func TestCraftFailsWithoutACraftingTableWithinReach(t *testing.T) {
	w := simWorld{}
	w.platform(-4, 4, -4, 4)
	// A table too far to click does not help.
	w[protocol.BlockPos{8, 0, 8}] = "crafting_table"
	s, tr := newCraftRig(t, w, mgl32.Vec3{0.5, 0, 0.5}, protocol.BlockPos{})
	tr.Apply(&packet.InventorySlot{WindowID: 0, Slot: 1, NewItem: protocol.ItemInstance{
		StackNetworkID: 42,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: craftPlanksID}, Count: 5},
	}})
	tr.Apply(&packet.InventorySlot{WindowID: 0, Slot: 2, NewItem: protocol.ItemInstance{
		StackNetworkID: 43,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: craftStickID}, Count: 3},
	}})

	sk := NewCraft(w, tr.Snapshot, "wooden_hoe", 1)
	st, err := runCraftErr(t, sk, s.a)
	if st != Failed {
		t.Fatalf("status = %v, want Failed", st)
	}
	if !strings.Contains(err.Error(), "needs crafting table") {
		t.Errorf("err = %v, want needs crafting table", err)
	}
	if len(s.requests()) != 0 {
		t.Error("a craft with no station still sent a request")
	}
}

func TestCraftFailsWhenTheIngredientsAreMissing(t *testing.T) {
	w := simWorld{}
	w.platform(-4, 4, -4, 4)
	s, tr := newCraftRig(t, w, mgl32.Vec3{0.5, 0, 0.5}, protocol.BlockPos{})
	tr.Apply(&packet.InventorySlot{WindowID: 0, Slot: 0, NewItem: protocol.ItemInstance{
		StackNetworkID: 9,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: idCobblestone}, Count: 32},
	}})

	sk := NewCraft(w, tr.Snapshot, "oak_planks", 4)
	st, err := runCraftErr(t, sk, s.a)
	if st != Failed {
		t.Fatalf("status = %v, want Failed", st)
	}
	if !errors.Is(err, act.ErrMissingIngredients) {
		t.Errorf("err = %v, want the missing ingredients", err)
	}
	if len(s.requests()) != 0 {
		t.Error("a craft that could not be paid for still sent a request")
	}
}

func TestCraftSkipsWorkWhenTheUnitAlreadyHoldsEnough(t *testing.T) {
	w := simWorld{}
	w.platform(-4, 4, -4, 4)
	s, tr := newCraftRig(t, w, mgl32.Vec3{0.5, 0, 0.5}, protocol.BlockPos{})
	tr.Apply(&packet.InventorySlot{WindowID: 0, Slot: 0, NewItem: protocol.ItemInstance{
		StackNetworkID: 42,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: craftPlanksID}, Count: 8},
	}})

	sk := NewCraft(w, tr.Snapshot, "oak_planks", 4)
	if st := runCraft(t, sk, s.a); st != Done {
		t.Fatalf("status = %v, want Done without crafting", st)
	}
	if len(s.requests()) != 0 {
		t.Error("a unit holding enough still crafted")
	}
}

func haveItems(tr *belief.Tracker, name string) int {
	n := 0
	for _, it := range tr.Snapshot().Inventory {
		if it.Name == name {
			n += it.Count
		}
	}
	return n
}

// runCraft steps the skill until it stops being Running, bounded so a skill
// that never finishes fails the test instead of hanging it.
func runCraft(t *testing.T, sk Skill, a Actuator) Status {
	t.Helper()
	st, _ := runCraftErr(t, sk, a)
	return st
}

func runCraftErr(t *testing.T, sk Skill, a Actuator) (Status, error) {
	t.Helper()
	ctx := context.Background()
	// A step that only waits for the server takes far less than a
	// millisecond, so the bound is time, not steps, with a breath between
	// steps for the server goroutine to answer in.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, err := sk.Step(ctx, a)
		if st != Running {
			return st, err
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the skill never finished")
	return Failed, nil
}
