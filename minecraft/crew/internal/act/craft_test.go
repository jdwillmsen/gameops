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
	craftOakLog         int32  = 5
	craftOakPlanks      int32  = 6
	craftStick          int32  = 7
	craftCraftingTable  int32  = 8
	craftWoodenHoe      int32  = 9
	recipePlanks        uint32 = 10
	recipeStick         uint32 = 11
	recipeTable         uint32 = 12
	recipeHoe           uint32 = 13
	firstPlacedStackID  int32  = 501
	secondPlacedStackID int32  = 502
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

// answering is a fake server that replies to the unit's stack requests from
// its own goroutine, as the read loop would, so a craft waiting for its
// answer crosses a real poll cycle. statuses is the queue of answers, one
// per request; once it is spent every request succeeds.
type answering struct {
	a    *Actuator
	s    *sink
	stop chan struct{}

	statuses []uint8
}

func startAnswering(a *Actuator, s *sink, statuses ...uint8) *answering {
	an := &answering{a: a, s: s, stop: make(chan struct{}), statuses: statuses}
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

func (an *answering) serve() {
	answered := 0
	for {
		select {
		case <-an.stop:
			return
		default:
		}
		reqs := stackRequests(an.s)
		for ; answered < len(reqs); answered++ {
			an.reply(reqs[answered], answered)
		}
		time.Sleep(time.Millisecond)
	}
}

// reply answers one request: OK answers name the destination slot's stack,
// the way the server's does, with a fresh ID per answer.
func (an *answering) reply(p *packet.ItemStackRequest, answered int) {
	status := StatusOK
	if len(an.statuses) > 0 {
		status = an.statuses[0]
		an.statuses = an.statuses[1:]
	}
	resp := protocol.ItemStackResponse{Status: status, RequestID: p.Requests[0].RequestID}
	if status == StatusOK {
		for _, action := range p.Requests[0].Actions {
			take, ok := action.(*protocol.TakeStackRequestAction)
			if !ok {
				continue
			}
			resp.ContainerInfo = []protocol.StackResponseContainerInfo{{
				Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
				SlotInfo: []protocol.StackResponseSlotInfo{{
					Slot:           take.Destination.Slot,
					StackNetworkID: firstPlacedStackID + int32(answered),
					Count:          take.Count,
				}},
			}}
		}
	}
	an.a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{resp}})
}

func stopAnswering(an *answering) { close(an.stop) }

// craftRequest returns the one request of the n-th craft call.
func craftRequest(t *testing.T, s *sink, n int) protocol.ItemStackRequest {
	t.Helper()
	reqs := stackRequests(s)
	if len(reqs) <= n {
		t.Fatalf("only %d stack requests were sent", len(reqs))
	}
	return reqs[n].Requests[0]
}

func TestCraftPlanksSendsTheRecipeConsumeAndTakeActions(t *testing.T) {
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
	r := craftRequest(t, s, 0)
	if r.RequestID != -3 || len(r.Actions) != 3 {
		t.Fatalf("request = %+v with %d actions", r, len(r.Actions))
	}
	cr, ok := r.Actions[0].(*protocol.CraftRecipeStackRequestAction)
	if !ok || cr.RecipeNetworkID != recipePlanks || cr.NumberOfCrafts != 1 {
		t.Errorf("craft action = %+v", r.Actions[0])
	}
	consume, ok := r.Actions[1].(*protocol.ConsumeStackRequestAction)
	if !ok || consume.Count != 1 ||
		consume.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		consume.Source.Slot != 3 || consume.Source.StackNetworkID != 41 {
		t.Errorf("consume = %+v, want one oak_log from slot 3", r.Actions[1])
	}
	take, ok := r.Actions[2].(*protocol.TakeStackRequestAction)
	if !ok || take.Count != 4 ||
		take.Source.Container.ContainerID != protocol.ContainerCreatedOutput || take.Source.Slot != createdOutputSlot ||
		take.Source.StackNetworkID != 0 ||
		take.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		take.Destination.Slot != 0 || take.Destination.StackNetworkID != 0 {
		t.Errorf("take = %+v, want the created output into the first empty slot", r.Actions[2])
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
	r := craftRequest(t, s, 0)
	cr, ok := r.Actions[0].(*protocol.CraftRecipeStackRequestAction)
	if !ok || cr.NumberOfCrafts != 2 {
		t.Errorf("craft action = %+v, want two crafts", r.Actions[0])
	}
	consume, ok := r.Actions[1].(*protocol.ConsumeStackRequestAction)
	if !ok || consume.Count != 2 {
		t.Errorf("consume = %+v, want both logs", r.Actions[1])
	}
	take, ok := r.Actions[2].(*protocol.TakeStackRequestAction)
	if !ok || take.Count != 8 {
		t.Errorf("take = %+v, want both results", r.Actions[2])
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
	take := craftRequest(t, s, 1).Actions[2].(*protocol.TakeStackRequestAction)
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
	r := craftRequest(t, s, 0)
	if r.RequestID != -3 || len(r.Actions) != 4 {
		t.Fatalf("request = %+v with %d actions", r, len(r.Actions))
	}
	cr, ok := r.Actions[0].(*protocol.CraftRecipeStackRequestAction)
	if !ok || cr.RecipeNetworkID != recipeHoe || cr.NumberOfCrafts != 1 {
		t.Errorf("craft action = %+v", r.Actions[0])
	}
	planks, ok := r.Actions[1].(*protocol.ConsumeStackRequestAction)
	if !ok || planks.Count != 2 || planks.Source.Slot != 1 || planks.Source.StackNetworkID != 42 {
		t.Errorf("planks consume = %+v", r.Actions[1])
	}
	sticks, ok := r.Actions[2].(*protocol.ConsumeStackRequestAction)
	if !ok || sticks.Count != 2 || sticks.Source.Slot != 2 || sticks.Source.StackNetworkID != 43 {
		t.Errorf("stick consume = %+v", r.Actions[2])
	}
	take, ok := r.Actions[3].(*protocol.TakeStackRequestAction)
	if !ok || take.Count != 1 ||
		take.Source.Container.ContainerID != protocol.ContainerCreatedOutput || take.Source.Slot != createdOutputSlot ||
		take.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		take.Destination.Slot != 0 || take.Destination.StackNetworkID != 0 {
		t.Errorf("take = %+v, want the hoe into the first empty slot", r.Actions[3])
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
	defer stopAnswering(startAnswering(a, s, StatusSlotOutOfRange))

	_, err := a.Craft("oak_planks", 1)
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("status %d", StatusSlotOutOfRange)) {
		t.Errorf("err = %v, want the response status in it", err)
	}
}

func TestCraftAvoidsADestinationTheServerRefused(t *testing.T) {
	inv := []belief.Item{{Slot: 3, Name: "oak_log", Count: 12, StackNetworkID: 41}}
	a, s := newCrafter(inv)
	defer stopAnswering(startAnswering(a, s, StatusOccupied))

	if _, err := a.Craft("oak_planks", 1); !errors.Is(err, ErrRejected) {
		t.Fatalf("first err = %v, want ErrRejected", err)
	}
	if _, err := a.Craft("oak_planks", 1); err != nil {
		t.Fatalf("second craft after a refused destination: %v", err)
	}
	take := craftRequest(t, s, 1).Actions[2].(*protocol.TakeStackRequestAction)
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
