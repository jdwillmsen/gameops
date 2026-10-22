package act

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/craft"
)

// Item network IDs and recipe network IDs of the fake server, written the
// way the vanilla one sends these recipes.
const (
	craftOakLog        int32  = 5
	craftOakPlanks     int32  = 6
	craftStick         int32  = 7
	craftCraftingTable int32  = 8
	craftWoodenHoe     int32  = 9
	recipePlanks       uint32 = 10
	recipeStick        uint32 = 11
	recipeTable        uint32 = 12
	recipeHoe          uint32 = 13
	firstPlacedStackID int32  = 501
	// gridStackID is the stack ID the fake server gives a grid cell, which
	// it derives from the slot so every cell has its own.
	gridStackID int32 = 700
	// renamedStackID is the stack ID the fake server gives a stack that
	// gave up items when it renames it.
	renamedStackID int32 = 800
)

var craftItemEntries = []protocol.ItemEntry{
	{Name: "minecraft:oak_log", RuntimeID: int16(craftOakLog)},
	{Name: "minecraft:oak_planks", RuntimeID: int16(craftOakPlanks)},
	{Name: "minecraft:stick", RuntimeID: int16(craftStick)},
	{Name: "minecraft:crafting_table", RuntimeID: int16(craftCraftingTable)},
	{Name: "minecraft:wooden_hoe", RuntimeID: int16(craftWoodenHoe)},
}

func craftDescriptor(name string, count int32) protocol.ItemDescriptorCount {
	return protocol.ItemDescriptorCount{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:" + name}, Count: count}
}

// craftBook holds the planks, sticks, crafting table and hoe recipes.
func craftBook() *craft.Book {
	empty := protocol.ItemDescriptorCount{Descriptor: &protocol.InvalidItemDescriptor{}}
	b := craft.NewBook()
	b.Store(&packet.CraftingData{
		ClearRecipes: true,
		ShapelessRecipes: []protocol.ShapelessRecipe{{
			RecipeID:        "oak_planks",
			Input:           []protocol.ItemDescriptorCount{craftDescriptor("oak_log", 1)},
			Output:          []protocol.ItemStack{{ItemType: protocol.ItemType{NetworkID: craftOakPlanks}, Count: 4}},
			RecipeNetworkID: recipePlanks,
		}},
		ShapedRecipes: []protocol.ShapedRecipe{
			{
				RecipeID: "stick", Width: 1, Height: 2,
				Input: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.ItemTagItemDescriptor{Tag: "minecraft:planks"}, Count: 1},
					{Descriptor: &protocol.ItemTagItemDescriptor{Tag: "minecraft:planks"}, Count: 1},
				},
				Output:          []protocol.ItemStack{{ItemType: protocol.ItemType{NetworkID: craftStick}, Count: 4}},
				RecipeNetworkID: recipeStick,
			},
			{
				RecipeID: "crafting_table", Width: 2, Height: 2,
				Input: []protocol.ItemDescriptorCount{
					craftDescriptor("oak_planks", 1), craftDescriptor("oak_planks", 1),
					craftDescriptor("oak_planks", 1), craftDescriptor("oak_planks", 1),
				},
				Output:          []protocol.ItemStack{{ItemType: protocol.ItemType{NetworkID: craftCraftingTable}, Count: 1}},
				RecipeNetworkID: recipeTable,
			},
			{
				RecipeID: "wooden_hoe", Width: 2, Height: 3,
				Input: []protocol.ItemDescriptorCount{
					craftDescriptor("oak_planks", 1), craftDescriptor("oak_planks", 1),
					empty, craftDescriptor("stick", 1),
					empty, craftDescriptor("stick", 1),
				},
				Output:          []protocol.ItemStack{{ItemType: protocol.ItemType{NetworkID: craftWoodenHoe}, Count: 1}},
				Block:           "crafting_table",
				RecipeNetworkID: recipeHoe,
			},
		},
	}, craftItemEntries)
	return b
}

// newCrafter is an actuator with the recipe book and a static inventory.
func newCrafter(inv []belief.Item) (*Actuator, *sink) {
	a, s := newAct(flat(), Pose{})
	a.ItemNames(craftItemEntries)
	a.Crafting(craftBook(), func() []belief.Item { return inv })
	return a, s
}

// taggedBook holds one recipe whose ingredient is the tag given, so a craft
// can be tried against a tag the client may or may not know.
func taggedBook(tag string) *craft.Book {
	b := craft.NewBook()
	b.Store(&packet.CraftingData{
		ClearRecipes: true,
		ShapelessRecipes: []protocol.ShapelessRecipe{{
			RecipeID: "stick",
			Input: []protocol.ItemDescriptorCount{{
				Descriptor: &protocol.ItemTagItemDescriptor{Tag: tag},
				Count:      2,
			}},
			Output:          []protocol.ItemStack{{ItemType: protocol.ItemType{NetworkID: craftStick}, Count: 4}},
			RecipeNetworkID: recipeStick,
		}},
	}, craftItemEntries)
	return b
}

// answering is a fake server that replies to the unit's stack requests from
// its own goroutine, as the read loop would, so a craft waiting for its
// answer crosses a real poll cycle. statuses is the queue of answers, one
// per request; once it is spent every request succeeds.
type answering struct {
	a    *Actuator
	s    *sink
	stop chan struct{}

	statuses []uint8
	// opened counts the inventory opens already answered.
	opened int
	// placed counts the stacks the server named, so every one of them gets
	// an ID of its own.
	placed int32
	// renamed makes the server name a stack that gave up items with a new
	// ID, the way a server may treat a stack that was split.
	renamed bool
	// left counts what each inventory slot holds, so the answers can name
	// what is left of a stack.
	left map[int]int
}

func startAnswering(a *Actuator, s *sink, statuses ...uint8) *answering {
	an := &answering{a: a, s: s, stop: make(chan struct{}), statuses: statuses}
	go an.serve()
	return an
}

// startRenaming answers as a server does that knows a stack which gave up
// items by a new ID.
func startRenaming(a *Actuator, s *sink, statuses ...uint8) *answering {
	an := &answering{a: a, s: s, stop: make(chan struct{}), statuses: statuses, renamed: true}
	go an.serve()
	return an
}

// stackRequests collects the requests sent so far.
func stackRequests(s *sink) []*packet.ItemStackRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*packet.ItemStackRequest
	for _, pk := range s.got {
		if r, ok := pk.(*packet.ItemStackRequest); ok {
			out = append(out, r)
		}
	}
	return out
}

// placements are the requests that put ingredients into the crafting grid.
func placements(s *sink) []protocol.ItemStackRequest {
	var out []protocol.ItemStackRequest
	for _, r := range stackRequests(s) {
		if len(r.Requests) == 0 || len(r.Requests[0].Actions) == 0 {
			continue
		}
		if _, ok := r.Requests[0].Actions[0].(*protocol.PlaceStackRequestAction); ok {
			out = append(out, r.Requests[0])
		}
	}
	return out
}

// crafts are the requests that ask the server to make the item.
func crafts(s *sink) []protocol.ItemStackRequest {
	var out []protocol.ItemStackRequest
	for _, r := range stackRequests(s) {
		if len(r.Requests) == 0 || len(r.Requests[0].Actions) == 0 {
			continue
		}
		if _, ok := r.Requests[0].Actions[0].(*protocol.CraftRecipeStackRequestAction); ok {
			out = append(out, r.Requests[0])
		}
	}
	return out
}

func (an *answering) serve() {
	answered := 0
	for {
		select {
		case <-an.stop:
			return
		default:
		}
		// The inventory screen opens on request, as the server opens it.
		an.s.mu.Lock()
		var opens int
		for _, pk := range an.s.got {
			if _, ok := pk.(*packet.Interact); ok {
				opens++
			}
		}
		an.s.mu.Unlock()
		for ; an.opened < opens; an.opened++ {
			an.a.Apply(&packet.ContainerOpen{WindowID: 0, ContainerType: 0xff /* ContainerTypeInventory, -1 as a byte */, ContainerEntityUniqueID: -1})
		}
		reqs := stackRequests(an.s)
		for ; answered < len(reqs); answered++ {
			an.reply(reqs[answered])
		}
		time.Sleep(time.Millisecond)
	}
}

// reply answers one request: a placement names the grid cell the items went
// to, and a craft names the destination slot's stack, the way the server's
// does, with a fresh ID per answer.
func (an *answering) reply(p *packet.ItemStackRequest) {
	status := StatusOK
	if len(an.statuses) > 0 {
		status = an.statuses[0]
		an.statuses = an.statuses[1:]
	}
	resp := protocol.ItemStackResponse{Status: status, RequestID: p.Requests[0].RequestID}
	if status == StatusOK {
		for _, action := range p.Requests[0].Actions {
			switch act := action.(type) {
			case *protocol.PlaceStackRequestAction:
				resp.ContainerInfo = append(resp.ContainerInfo, protocol.StackResponseContainerInfo{
					Container: protocol.FullContainerName{ContainerID: protocol.ContainerCraftingInput},
					SlotInfo: []protocol.StackResponseSlotInfo{{
						Slot:           act.Destination.Slot,
						StackNetworkID: gridStackID + int32(act.Destination.Slot),
						Count:          act.Count,
					}},
				})
				// The stack that paid for the cell has that many items
				// less, and the server may know it by a new ID.
				resp.ContainerInfo = append(resp.ContainerInfo, protocol.StackResponseContainerInfo{
					Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
					SlotInfo: []protocol.StackResponseSlotInfo{{
						Slot:           act.Source.Slot,
						StackNetworkID: an.sourceID(act.Source),
						Count:          byte(an.remaining(act.Source, int(act.Count))),
					}},
				})
			case *protocol.TakeStackRequestAction:
				an.placed++
				resp.ContainerInfo = append(resp.ContainerInfo, protocol.StackResponseContainerInfo{
					Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
					SlotInfo: []protocol.StackResponseSlotInfo{{
						Slot:           act.Destination.Slot,
						StackNetworkID: firstPlacedStackID + an.placed - 1,
						Count:          act.Count,
					}},
				})
			}
		}
	}
	an.a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{resp}})
}

func stopAnswering(an *answering) { close(an.stop) }

// sourceID is the ID the server knows the source stack by: the one the
// request quoted, unless the server treats a stack that gave up items as a
// new one.
func (an *answering) sourceID(src protocol.StackRequestSlotInfo) int32 {
	if an.renamed {
		return renamedStackID + int32(src.Slot)
	}
	return src.StackNetworkID
}

// remaining is how many items are left in the source stack.
func (an *answering) remaining(src protocol.StackRequestSlotInfo, moved int) int {
	if an.left == nil {
		an.left = map[int]int{}
		if an.a.invOf != nil {
			for _, st := range an.a.invOf() {
				an.left[st.Slot] = st.Count
			}
		}
	}
	slot := int(src.Slot)
	an.left[slot] -= moved
	if an.left[slot] < 0 {
		an.left[slot] = 0
	}
	return an.left[slot]
}

func TestCraftFillsTheGridBeforeItAsksForTheCraft(t *testing.T) {
	inv := []belief.Item{{Slot: 3, Name: "oak_log", Count: 12, StackNetworkID: 41}}
	a, s := newCrafter(inv)
	defer stopAnswering(startAnswering(a, s))

	made, err := a.Craft("oak_planks", 1)
	if err != nil {
		t.Fatal(err)
	}
	if made != 4 {
		t.Errorf("made = %d, want 4 planks", made)
	}
	// The server only accepts a craft whose ingredients already sit in the
	// grid, so the log goes in first.
	placed := placements(s)
	if len(placed) != 1 {
		t.Fatalf("placed %d ingredients, want the one the recipe takes", len(placed))
	}
	if len(placed[0].Actions) != 1 {
		t.Fatalf("placement = %+v, want one action", placed[0])
	}
	move, ok := placed[0].Actions[0].(*protocol.PlaceStackRequestAction)
	if !ok {
		t.Fatalf("placement action = %+v, want a place", placed[0].Actions[0])
	}
	if move.Count != 1 ||
		move.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		move.Source.Slot != 3 || move.Source.StackNetworkID != 41 {
		t.Errorf("placement source = %+v, want the oak_log of slot 3", move)
	}
	if move.Destination.Container.ContainerID != protocol.ContainerCraftingInput ||
		move.Destination.Slot != 28 || move.Destination.StackNetworkID != 0 {
		t.Errorf("placement destination = %+v, want the player's first grid cell", move.Destination)
	}

	cr := crafts(s)
	if len(cr) != 1 {
		t.Fatalf("sent %d craft requests, want 1", len(cr))
	}
	r := cr[0]
	if len(r.Actions) != 4 {
		t.Fatalf("craft request = %+v with %d actions", r, len(r.Actions))
	}
	recipe, ok := r.Actions[0].(*protocol.CraftRecipeStackRequestAction)
	if !ok || recipe.RecipeNetworkID != recipePlanks || recipe.NumberOfCrafts != 1 {
		t.Fatalf("craft action = %+v", r.Actions[0])
	}
	results, ok := r.Actions[1].(*protocol.CraftResultsDeprecatedStackRequestAction)
	if !ok || results.TimesCrafted != 1 || len(results.ResultItems) != 1 {
		t.Fatalf("results action = %+v", r.Actions[1])
	}
	if got := results.ResultItems[0]; got.Identifier != "minecraft:oak_planks" || got.Count != 4 {
		t.Errorf("craft result = %+v, want four oak_planks", got)
	}
	consume, ok := r.Actions[2].(*protocol.ConsumeStackRequestAction)
	if !ok || consume.Count != 1 ||
		consume.Source.Container.ContainerID != protocol.ContainerCraftingInput ||
		consume.Source.Slot != 28 ||
		consume.Source.StackNetworkID != gridStackID+28 {
		t.Errorf("consume = %+v, want the log the placement put in cell 28", r.Actions[2])
	}
	take, ok := r.Actions[3].(*protocol.TakeStackRequestAction)
	if !ok || take.Count != 4 ||
		take.Source.Container.ContainerID != protocol.ContainerCreatedOutput || take.Source.Slot != createdOutputSlot ||
		take.Source.StackNetworkID != r.RequestID ||
		take.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		take.Destination.Slot != 0 || take.Destination.StackNetworkID != 0 {
		t.Errorf("take = %+v, want the created output into the first empty slot", r.Actions[3])
	}
}

func TestCraftSticksFromPlanksOfAnyWood(t *testing.T) {
	// The stick recipe asks for the planks tag, so any planks the unit
	// holds pay for it.
	inv := []belief.Item{{Slot: 4, Name: "oak_planks", Count: 8, StackNetworkID: 44}}
	a, s := newCrafter(inv)
	defer stopAnswering(startAnswering(a, s))

	made, err := a.Craft("stick", 1)
	if err != nil {
		t.Fatal(err)
	}
	if made != 4 {
		t.Errorf("made = %d, want 4 sticks", made)
	}
	// The recipe is one wide and two high, so its cells are a row of the
	// grid apart.
	placed := placements(s)
	if len(placed) != 2 {
		t.Fatalf("placed %d ingredients, want the recipe's two cells", len(placed))
	}
	for i, want := range []byte{28, 30} {
		move, ok := placed[i].Actions[0].(*protocol.PlaceStackRequestAction)
		if !ok {
			t.Fatalf("placement %d = %+v, want a place", i, placed[i].Actions[0])
		}
		if move.Count != 1 ||
			move.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
			move.Source.Slot != 4 || move.Source.StackNetworkID != 44 {
			t.Errorf("placement %d source = %+v, want the planks of slot 4", i, move)
		}
		if move.Destination.Container.ContainerID != protocol.ContainerCraftingInput ||
			move.Destination.Slot != want {
			t.Errorf("placement %d destination = %+v, want cell %d", i, move.Destination, want)
		}
	}
	cr := crafts(s)
	if len(cr) != 1 || len(cr[0].Actions) != 5 {
		t.Fatalf("craft requests = %+v, want one with two consumes", cr)
	}
	for i, want := range []byte{28, 30} {
		consume, ok := cr[0].Actions[2+i].(*protocol.ConsumeStackRequestAction)
		if !ok || consume.Count != 1 ||
			consume.Source.Container.ContainerID != protocol.ContainerCraftingInput ||
			consume.Source.Slot != want ||
			consume.Source.StackNetworkID != gridStackID+int32(want) {
			t.Errorf("consume %d = %+v, want the planks of cell %d", i, cr[0].Actions[2+i], want)
		}
	}
}

func TestCraftQuotesTheStackIDTheServerGaveTheSourceStack(t *testing.T) {
	// A stack that gave up items may be a new stack, and the placement
	// that follows has to quote the ID the server knows it by.
	inv := []belief.Item{{Slot: 4, Name: "oak_planks", Count: 8, StackNetworkID: 44}}
	a, s := newCrafter(inv)
	defer stopAnswering(startRenaming(a, s))

	if _, err := a.Craft("stick", 1); err != nil {
		t.Fatal(err)
	}
	placed := placements(s)
	if len(placed) != 2 {
		t.Fatalf("placed %d ingredients, want the recipe's two cells", len(placed))
	}
	first := placed[0].Actions[0].(*protocol.PlaceStackRequestAction)
	if first.Source.StackNetworkID != 44 {
		t.Errorf("first placement = %+v, want the stack's own ID", first.Source)
	}
	second := placed[1].Actions[0].(*protocol.PlaceStackRequestAction)
	if second.Source.StackNetworkID != renamedStackID+4 {
		t.Errorf("second placement = %+v, want the ID the answer gave the stack", second.Source)
	}
}

func TestCraftFailsWhenTheTagIsOneTheClientDoesNotKnow(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	a.ItemNames(craftItemEntries)
	a.Crafting(taggedBook("minecraft:wool"), func() []belief.Item {
		return []belief.Item{{Slot: 4, Name: "white_wool", Count: 8, StackNetworkID: 44}}
	})

	if _, err := a.Craft("stick", 1); !errors.Is(err, ErrMissingIngredients) {
		t.Fatalf("err = %v, want ErrMissingIngredients", err)
	}
	if len(s.got) != 0 {
		t.Error("a craft the client cannot match still sent a packet")
	}
}

func TestCraftedItemsAreHeldForPlacement(t *testing.T) {
	inv := []belief.Item{{Slot: 3, Name: "oak_log", Count: 12, StackNetworkID: 41}}
	a, s := newCrafter(inv)
	defer stopAnswering(startAnswering(a, s))

	if _, err := a.Craft("oak_planks", 1); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if got := a.inv[0]; got.Stack.NetworkID != a.itemIDs["oak_planks"] || got.Stack.Count != 4 {
		t.Errorf("slot 0 = %+v, want the 4 crafted planks a placement can hold", got)
	}
}

func TestCraftScalesOneRequestOverSeveralCrafts(t *testing.T) {
	inv := []belief.Item{{Slot: 3, Name: "oak_log", Count: 12, StackNetworkID: 41}}
	a, s := newCrafter(inv)
	defer stopAnswering(startAnswering(a, s))

	made, err := a.Craft("oak_planks", 2)
	if err != nil {
		t.Fatal(err)
	}
	if made != 8 {
		t.Errorf("made = %d, want 8 planks", made)
	}
	placed := placements(s)
	if len(placed) != 1 {
		t.Fatalf("placed %d ingredients, want the one cell of the recipe", len(placed))
	}
	move := placed[0].Actions[0].(*protocol.PlaceStackRequestAction)
	if move.Count != 2 {
		t.Errorf("placement = %+v, want both logs", move)
	}
	cr := crafts(s)
	if len(cr) != 1 {
		t.Fatalf("sent %d craft requests, want 1", len(cr))
	}
	r := cr[0]
	recipe, ok := r.Actions[0].(*protocol.CraftRecipeStackRequestAction)
	if !ok || recipe.NumberOfCrafts != 2 {
		t.Errorf("craft action = %+v, want two crafts", r.Actions[0])
	}
	results, ok := r.Actions[1].(*protocol.CraftResultsDeprecatedStackRequestAction)
	if !ok || results.TimesCrafted != 2 || len(results.ResultItems) != 1 {
		t.Fatalf("results action = %+v", r.Actions[1])
	}
	if got := results.ResultItems[0]; got.Identifier != "minecraft:oak_planks" || got.Count != 8 {
		t.Errorf("craft result = %+v, want eight oak_planks", got)
	}
	consume, ok := r.Actions[2].(*protocol.ConsumeStackRequestAction)
	if !ok || consume.Count != 2 {
		t.Errorf("consume = %+v, want both logs", r.Actions[2])
	}
	take, ok := r.Actions[3].(*protocol.TakeStackRequestAction)
	if !ok || take.Count != 8 {
		t.Errorf("take = %+v, want both results", r.Actions[3])
	}
}

func TestCraftMergesOntoTheStackItPlacedItself(t *testing.T) {
	inv := []belief.Item{{Slot: 3, Name: "oak_log", Count: 12, StackNetworkID: 41}}
	a, s := newCrafter(inv)
	defer stopAnswering(startAnswering(a, s))

	if _, err := a.Craft("oak_planks", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Craft("oak_planks", 1); err != nil {
		t.Fatal(err)
	}
	// The first answer put a stack in slot 0 with an ID only it knew; the
	// second craft merges onto that stack instead of colliding with it.
	cr := crafts(s)
	if len(cr) != 2 {
		t.Fatalf("sent %d craft requests, want 2", len(cr))
	}
	take := cr[1].Actions[3].(*protocol.TakeStackRequestAction)
	if take.Destination.Slot != 0 || take.Destination.StackNetworkID != firstPlacedStackID {
		t.Errorf("second take = %+v, want slot 0 with the placed stack's ID", take)
	}
}

func TestCraftHoeThroughAnOpenCraftingTable(t *testing.T) {
	inv := []belief.Item{
		{Slot: 1, Name: "oak_planks", Count: 5, StackNetworkID: 42},
		{Slot: 2, Name: "stick", Count: 3, StackNetworkID: 43},
	}
	a, s := newCrafter(inv)
	defer stopAnswering(startAnswering(a, s))
	a.Apply(&packet.ContainerOpen{
		WindowID:          6,
		ContainerType:     protocol.ContainerTypeWorkbench,
		ContainerPosition: protocol.BlockPos{2, 0, 1},
	})

	made, err := a.Craft("wooden_hoe", 1)
	if err != nil {
		t.Fatal(err)
	}
	if made != 1 {
		t.Errorf("made = %d, want one hoe", made)
	}
	// The hoe is two wide and three high, so it sits at the top-left of the
	// table's 3x3 grid and its rows are three slots apart.
	placed := placements(s)
	if len(placed) != 4 {
		t.Fatalf("placed %d ingredients, want the recipe's four cells", len(placed))
	}
	want := []struct {
		slot  byte
		from  byte
		stack int32
	}{
		{32, 1, 42}, {33, 1, 42}, {36, 2, 43}, {39, 2, 43},
	}
	for i, w := range want {
		move, ok := placed[i].Actions[0].(*protocol.PlaceStackRequestAction)
		if !ok {
			t.Fatalf("placement %d = %+v, want a place", i, placed[i].Actions[0])
		}
		if move.Count != 1 ||
			move.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
			move.Source.Slot != w.from || move.Source.StackNetworkID != w.stack {
			t.Errorf("placement %d source = %+v, want the stack of slot %d", i, move, w.from)
		}
		if move.Destination.Container.ContainerID != protocol.ContainerCraftingInput ||
			move.Destination.Slot != w.slot {
			t.Errorf("placement %d destination = %+v, want cell %d", i, move.Destination, w.slot)
		}
	}
	cr := crafts(s)
	if len(cr) != 1 {
		t.Fatalf("sent %d craft requests, want 1", len(cr))
	}
	r := cr[0]
	if len(r.Actions) != 7 {
		t.Fatalf("craft request = %+v with %d actions", r, len(r.Actions))
	}
	recipe, ok := r.Actions[0].(*protocol.CraftRecipeStackRequestAction)
	if !ok || recipe.RecipeNetworkID != recipeHoe || recipe.NumberOfCrafts != 1 {
		t.Fatalf("craft action = %+v", r.Actions[0])
	}
	results, ok := r.Actions[1].(*protocol.CraftResultsDeprecatedStackRequestAction)
	if !ok || results.TimesCrafted != 1 || len(results.ResultItems) != 1 {
		t.Fatalf("results action = %+v", r.Actions[1])
	}
	if got := results.ResultItems[0]; got.Identifier != "minecraft:wooden_hoe" || got.Count != 1 {
		t.Errorf("craft result = %+v, want one wooden_hoe", got)
	}
	for i, w := range want {
		consume, ok := r.Actions[2+i].(*protocol.ConsumeStackRequestAction)
		if !ok || consume.Count != 1 ||
			consume.Source.Container.ContainerID != protocol.ContainerCraftingInput ||
			consume.Source.Slot != w.slot ||
			consume.Source.StackNetworkID != gridStackID+int32(w.slot) {
			t.Errorf("consume %d = %+v, want the cell %d of the grid", i, r.Actions[2+i], w.slot)
		}
	}
	take, ok := r.Actions[6].(*protocol.TakeStackRequestAction)
	if !ok || take.Count != 1 ||
		take.Source.Container.ContainerID != protocol.ContainerCreatedOutput || take.Source.Slot != createdOutputSlot ||
		take.Source.StackNetworkID != r.RequestID ||
		take.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		take.Destination.Slot != 0 || take.Destination.StackNetworkID != 0 {
		t.Errorf("take = %+v, want the hoe into the first empty slot", r.Actions[6])
	}
}

func TestCraftNeedsATableForA3x3Recipe(t *testing.T) {
	inv := []belief.Item{
		{Slot: 1, Name: "oak_planks", Count: 5, StackNetworkID: 42},
		{Slot: 2, Name: "stick", Count: 3, StackNetworkID: 43},
	}
	a, s := newCrafter(inv)

	_, err := a.Craft("wooden_hoe", 1)
	if !errors.Is(err, ErrNeedsTable) {
		t.Fatalf("err = %v, want ErrNeedsTable", err)
	}
	if len(s.got) != 0 {
		t.Error("a craft without its station still sent a packet")
	}

	// A chest window is not a crafting table.
	a.Apply(&packet.ContainerOpen{WindowID: 9, ContainerType: protocol.ContainerTypeContainer, ContainerPosition: protocol.BlockPos{4, 0, 1}})
	if _, err := a.Craft("wooden_hoe", 1); !errors.Is(err, ErrNeedsTable) {
		t.Fatalf("err through a chest = %v, want ErrNeedsTable", err)
	}
}

func TestCraftFailsOnMissingIngredients(t *testing.T) {
	a, s := newCrafter([]belief.Item{{Slot: 0, Name: "cobblestone", Count: 32, StackNetworkID: 9}})
	if _, err := a.Craft("oak_planks", 1); !errors.Is(err, ErrMissingIngredients) {
		t.Fatalf("err with nothing to craft from = %v, want ErrMissingIngredients", err)
	}
	// Three planks do not pay for a crafting table.
	b, s2 := newCrafter([]belief.Item{{Slot: 0, Name: "oak_planks", Count: 3, StackNetworkID: 10}})
	if _, err := b.Craft("crafting_table", 1); !errors.Is(err, ErrMissingIngredients) {
		t.Fatalf("err short of ingredients = %v, want ErrMissingIngredients", err)
	}
	if len(s.got) != 0 || len(s2.got) != 0 {
		t.Error("a craft that could not be paid for still sent a packet")
	}
}

func TestCraftFailsWithoutARecipe(t *testing.T) {
	a, s := newCrafter([]belief.Item{{Slot: 0, Name: "oak_log", Count: 12, StackNetworkID: 41}})
	if _, err := a.Craft("oak_door", 1); !errors.Is(err, ErrNoRecipe) {
		t.Fatalf("err = %v, want ErrNoRecipe", err)
	}
	if len(s.got) != 0 {
		t.Error("a craft with no recipe still sent a packet")
	}
	// A craft of nothing, or of more crafts than a byte holds, is not a
	// craft; no answer is needed to know.
	if _, err := a.Craft("oak_planks", 0); err == nil {
		t.Error("zero crafts accepted")
	}
	if _, err := a.Craft("oak_planks", 256); err == nil {
		t.Error("256 crafts accepted")
	}
}

func TestCraftRejectedIncludesTheResponseStatus(t *testing.T) {
	inv := []belief.Item{{Slot: 3, Name: "oak_log", Count: 12, StackNetworkID: 41}}
	a, s := newCrafter(inv)
	// The ingredient goes in, and the craft that follows is refused.
	defer stopAnswering(startAnswering(a, s, StatusOK, StatusSlotOutOfRange))

	_, err := a.Craft("oak_planks", 1)
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("status %d", StatusSlotOutOfRange)) {
		t.Errorf("err = %v, want the response status in it", err)
	}
}

func TestCraftGivesTheGridBackWhenTheCraftIsRefused(t *testing.T) {
	inv := []belief.Item{{Slot: 3, Name: "oak_log", Count: 12, StackNetworkID: 41}}
	a, s := newCrafter(inv)
	defer stopAnswering(startAnswering(a, s, StatusOK, StatusSlotOutOfRange))

	if _, err := a.Craft("oak_planks", 1); !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
	// Nothing may be stranded in the grid, so the log comes back.
	reqs := stackRequests(s)
	if len(reqs) != 3 {
		t.Fatalf("sent %d requests, want the placement, the craft and the give-back", len(reqs))
	}
	back := reqs[2].Requests[0]
	if len(back.Actions) != 1 {
		t.Fatalf("give-back = %+v, want one action", back)
	}
	take, ok := back.Actions[0].(*protocol.TakeStackRequestAction)
	if !ok || take.Count != 1 ||
		take.Source.Container.ContainerID != protocol.ContainerCraftingInput ||
		take.Source.Slot != 28 || take.Source.StackNetworkID != gridStackID+28 {
		t.Errorf("give-back source = %+v, want the log in cell 28", back.Actions[0])
	}
	if take.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		take.Destination.Slot != 0 || take.Destination.StackNetworkID != 0 {
		t.Errorf("give-back destination = %+v, want the first empty inventory slot", take.Destination)
	}
}

func TestCraftAvoidsADestinationTheServerRefused(t *testing.T) {
	inv := []belief.Item{{Slot: 3, Name: "oak_log", Count: 12, StackNetworkID: 41}}
	a, s := newCrafter(inv)
	// The first craft's destination is refused, the second one goes through.
	defer stopAnswering(startAnswering(a, s, StatusOK, StatusOccupied, StatusOK, StatusOK))

	if _, err := a.Craft("oak_planks", 1); !errors.Is(err, ErrRejected) {
		t.Fatalf("first err = %v, want ErrRejected", err)
	}
	if _, err := a.Craft("oak_planks", 1); err != nil {
		t.Fatalf("second craft after a refused destination: %v", err)
	}
	cr := crafts(s)
	if len(cr) != 2 {
		t.Fatalf("sent %d craft requests, want 2", len(cr))
	}
	take := cr[1].Actions[3].(*protocol.TakeStackRequestAction)
	if take.Destination.Slot != 1 {
		t.Errorf("second take = %+v, want it to move past the refused slot 0", take)
	}
}

func TestCreatedOutputSlotPinsTheValueToCheckOnTheLab(t *testing.T) {
	// Recent vanilla clients take the crafted result from slot 50 of
	// ContainerCreatedOutput. If the lab rejects a craft, this pin is the
	// one place to change, and this test is what forces the change to be
	// deliberate.
	if createdOutputSlot != 50 {
		t.Fatalf("createdOutputSlot = %d, want 50", createdOutputSlot)
	}
}

func TestCraftingGridSlotsPinTheValuesToCheckOnTheLab(t *testing.T) {
	// The player's own 2x2 grid sits in slots 28 to 31 of the crafting
	// input and a crafting table's 3x3 grid in slots 32 to 40.
	if playerGridBase != 28 || playerGridWidth != 2 {
		t.Errorf("player grid = %d wide %d, want 28 and 2", playerGridBase, playerGridWidth)
	}
	if tableGridBase != 32 || tableGridWidth != 3 {
		t.Errorf("table grid = %d wide %d, want 32 and 3", tableGridBase, tableGridWidth)
	}
}

func TestALateInventoryScreenIsClosedOnArrival(t *testing.T) {
	a, s := newCrafter(nil)
	a.mu.Lock()
	a.crafts.lateInventory = true
	a.mu.Unlock()
	a.Apply(&packet.ContainerOpen{WindowID: 0, ContainerType: inventoryWindow, ContainerEntityUniqueID: -1})
	a.mu.Lock()
	open := a.cont.open
	a.mu.Unlock()
	if open {
		t.Error("the late inventory screen is tracked as open")
	}
	if _, ok := s.last(t).(*packet.ContainerClose); !ok {
		t.Error("the late inventory screen was not closed")
	}
}

func TestAnOpenTableMakesA2x2Recipe(t *testing.T) {
	r := craft.Recipe{Block: "crafting_table"}
	if g := gridFor(r, true); g.base != tableGridBase {
		t.Errorf("grid base = %d with a table open, want the table's %d", g.base, tableGridBase)
	}
	if g := gridFor(r, false); g.base != playerGridBase {
		t.Errorf("grid base = %d with no table, want the player's %d", g.base, playerGridBase)
	}
}
