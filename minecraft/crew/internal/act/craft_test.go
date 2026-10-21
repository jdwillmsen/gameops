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

// ingredientName is the item or tag a recipe ingredient descriptor names.
func ingredientName(d protocol.ItemDescriptorCount) string {
	switch desc := d.Descriptor.(type) {
	case *protocol.DefaultItemDescriptor:
		return desc.Name
	case *protocol.ItemTagItemDescriptor:
		return desc.Tag
	}
	return ""
}

func TestCraftPlanksSendsTheRecipeBookActions(t *testing.T) {
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
	if r.RequestID != -3 || len(r.Actions) != 4 {
		t.Fatalf("request = %+v with %d actions", r, len(r.Actions))
	}
	// The shape is the one the vanilla client sends for a recipe book craft:
	// the auto craft, the results it makes, the consumes and the take.
	auto, ok := r.Actions[0].(*protocol.AutoCraftRecipeStackRequestAction)
	if !ok || auto.RecipeNetworkID != recipePlanks || auto.NumberOfCrafts != 1 {
		t.Fatalf("craft action = %+v", r.Actions[0])
	}
	if len(auto.Ingredients) != 1 {
		t.Fatalf("craft action = %+v, want the recipe's one ingredient", r.Actions[0])
	}
	if name := ingredientName(auto.Ingredients[0]); name != "minecraft:oak_log" || auto.Ingredients[0].Count != 1 {
		t.Errorf("craft ingredient = %+v, want one oak_log", auto.Ingredients[0])
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
		consume.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		consume.Source.Slot != 3 || consume.Source.StackNetworkID != 41 {
		t.Errorf("consume = %+v, want one oak_log from slot 3", r.Actions[2])
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
	r := craftRequest(t, s, 0)
	if len(r.Actions) != 4 {
		t.Fatalf("request = %+v with %d actions", r, len(r.Actions))
	}
	auto, ok := r.Actions[0].(*protocol.AutoCraftRecipeStackRequestAction)
	if !ok || auto.RecipeNetworkID != recipeStick {
		t.Fatalf("craft action = %+v", r.Actions[0])
	}
	if len(auto.Ingredients) != 2 {
		t.Fatalf("craft action = %+v, want the recipe's two planks cells", r.Actions[0])
	}
	for _, ing := range auto.Ingredients {
		if name := ingredientName(ing); name != "minecraft:planks" || ing.Count != 1 {
			t.Errorf("craft ingredient = %+v, want one planks cell", ing)
		}
	}
	consume, ok := r.Actions[2].(*protocol.ConsumeStackRequestAction)
	if !ok || consume.Count != 2 ||
		consume.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		consume.Source.Slot != 4 || consume.Source.StackNetworkID != 44 {
		t.Errorf("consume = %+v, want two planks from slot 4", r.Actions[2])
	}
	take, ok := r.Actions[3].(*protocol.TakeStackRequestAction)
	if !ok || take.Count != 4 {
		t.Errorf("take = %+v, want the four sticks", r.Actions[3])
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

func TestCraftMovesToTheNextShapeOnAnInvalidCraft(t *testing.T) {
	inv := []belief.Item{{Slot: 3, Name: "oak_log", Count: 12, StackNetworkID: 41}}
	a, s := newCrafter(inv)
	invalid := uint8(protocol.ItemStackResponseStatusInvalidCraftRequest)
	defer stopAnswering(startAnswering(a, s, invalid, invalid))

	if _, err := a.Craft("oak_planks", 1); err != nil {
		t.Fatal(err)
	}
	if got := len(stackRequests(s)); got != 3 {
		t.Fatalf("sent %d requests, want two refused shapes and the accepted third", got)
	}
	if _, ok := craftRequest(t, s, 2).Actions[0].(*protocol.CraftRecipeStackRequestAction); !ok {
		t.Error("the third request is not shape 2's plain craft action")
	}
	if a.crafts.shape != 2 {
		t.Errorf("shape = %d, want the accepted 2 kept", a.crafts.shape)
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
	auto, ok := r.Actions[0].(*protocol.AutoCraftRecipeStackRequestAction)
	if !ok || auto.NumberOfCrafts != 2 {
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
	take := craftRequest(t, s, 1).Actions[3].(*protocol.TakeStackRequestAction)
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
	if r.RequestID != -3 || len(r.Actions) != 5 {
		t.Fatalf("request = %+v with %d actions", r, len(r.Actions))
	}
	auto, ok := r.Actions[0].(*protocol.AutoCraftRecipeStackRequestAction)
	if !ok || auto.RecipeNetworkID != recipeHoe || auto.NumberOfCrafts != 1 {
		t.Fatalf("craft action = %+v", r.Actions[0])
	}
	if len(auto.Ingredients) != 4 {
		t.Errorf("craft action = %+v, want the recipe's four ingredient cells", r.Actions[0])
	}
	results, ok := r.Actions[1].(*protocol.CraftResultsDeprecatedStackRequestAction)
	if !ok || results.TimesCrafted != 1 || len(results.ResultItems) != 1 {
		t.Fatalf("results action = %+v", r.Actions[1])
	}
	if got := results.ResultItems[0]; got.Identifier != "minecraft:wooden_hoe" || got.Count != 1 {
		t.Errorf("craft result = %+v, want one wooden_hoe", got)
	}
	planks, ok := r.Actions[2].(*protocol.ConsumeStackRequestAction)
	if !ok || planks.Count != 2 || planks.Source.Slot != 1 || planks.Source.StackNetworkID != 42 {
		t.Errorf("planks consume = %+v", r.Actions[2])
	}
	sticks, ok := r.Actions[3].(*protocol.ConsumeStackRequestAction)
	if !ok || sticks.Count != 2 || sticks.Source.Slot != 2 || sticks.Source.StackNetworkID != 43 {
		t.Errorf("stick consume = %+v", r.Actions[3])
	}
	take, ok := r.Actions[4].(*protocol.TakeStackRequestAction)
	if !ok || take.Count != 1 ||
		take.Source.Container.ContainerID != protocol.ContainerCreatedOutput || take.Source.Slot != createdOutputSlot ||
		take.Source.StackNetworkID != r.RequestID ||
		take.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		take.Destination.Slot != 0 || take.Destination.StackNetworkID != 0 {
		t.Errorf("take = %+v, want the hoe into the first empty slot", r.Actions[4])
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
	take := craftRequest(t, s, 1).Actions[3].(*protocol.TakeStackRequestAction)
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
