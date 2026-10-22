package act

import (
	"errors"
	"fmt"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/craft"
)

// Errors a craft can fail with, so a skill can tell what to do next: a
// missing table can be opened, anything else ends the task.
var (
	ErrNoRecipe           = errors.New("no recipe for the item")
	ErrMissingIngredients = errors.New("missing ingredients")
	ErrNeedsTable         = errors.New("needs crafting table")
	ErrRejected           = errors.New("server rejected the request")
)

// createdOutputSlot is the slot of ContainerCreatedOutput that the take
// reads. gophertunnel does not define it; recent vanilla clients send 50.
// It must be checked on the lab, and this constant is the one place to
// change it once it is known.
const createdOutputSlot = 50

// The crafting grid's slots. The server names the grid ContainerCraftingInput
// in both windows: the player's own 2x2 grid sits in slots 28 to 31 and a
// crafting table's 3x3 grid in slots 32 to 40.
const (
	playerGridBase  = 28
	playerGridWidth = 2
	tableGridBase   = 32
	tableGridWidth  = 3
)

const (
	// NumberOfCrafts is one byte wide.
	maxCrafts = 255
	// The crafted result has to land in one stack, and the item table
	// carries no stack limit, so 64 is assumed as Deposit does.
	maxCraftOutput = 64
	// Craft blocks the tick that would keep the unit alive on the server,
	// so its wait for an answer stays well under the no-input timeout. A
	// loaded lab server answers item requests in milliseconds.
	craftPoll     = 10 * time.Millisecond
	craftAnswerIn = 2 * time.Second
	// A craft sends one request per grid cell and one more for the craft
	// itself, and the unit sends no input while it waits, so the whole
	// craft is bounded.
	craftDeadline = 10 * time.Second
	// inventoryOpenIn bounds the wait for the inventory screen to open.
	inventoryOpenIn = time.Second
)

// Crafting gives the actuator the session's recipe book and a read of the
// unit's inventory, which between them decide what can be made.
func (a *Actuator) Crafting(b *craft.Book, inv func() []belief.Item) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.book, a.invOf = b, inv
}

// Craft makes times crafts of the item from the recipe the unit's inventory
// can already pay for, waiting for the server's answer, and returns how
// many of the item that made. A 2x2 recipe is made anywhere; a 3x3 one only
// through an open crafting table window.
func (a *Actuator) Craft(item string, times int) (int, error) {
	if times < 1 || times > maxCrafts {
		return 0, fmt.Errorf("cannot craft %d times", times)
	}
	plan, err := a.planCraft(item, times)
	if err != nil {
		return 0, err
	}
	// The server only accepts a craft whose ingredients already sit in the
	// crafting grid, so the grid is filled before the recipe is sent.
	deadline := time.Now().Add(craftDeadline)
	// The player's 2x2 grid belongs to the inventory screen, which a player
	// has open while crafting in it, so the unit opens it first and closes
	// it after.
	if plan.grid.base == playerGridBase {
		opened := a.openInventory(deadline)
		a.mu.Lock()
		window, kind := a.cont.window, a.cont.kind
		a.mu.Unlock()
		a.craftLog("craft_inventory", map[string]any{"opened": opened, "window": window, "kind": kind})
		if opened {
			defer a.CloseContainer()
		}
	}
	filled, err := a.fillGrid(plan, deadline)
	if err != nil {
		// Whatever went into the grid before the refusal goes back, so
		// nothing is stranded there.
		a.giveBack(plan, filled)
		return 0, fmt.Errorf("filling the crafting grid: %w", err)
	}
	made, err := a.sendCraft(plan, filled, deadline)
	if err != nil {
		return 0, fmt.Errorf("crafting from the grid: %w", err)
	}
	return made, nil
}

// openInventory asks the server to open the unit's own inventory screen and
// waits for the window, reporting whether this call opened it. A window
// already open is left as it is.
func (a *Actuator) openInventory(deadline time.Time) bool {
	a.mu.Lock()
	if a.cont.open && a.cont.kind == inventoryWindow {
		a.mu.Unlock()
		return false
	}
	if a.cont.open {
		// Another window, such as a chest, stands in the way of the
		// inventory screen, and nothing else is using it mid-craft.
		_ = a.w.WritePacket(&packet.ContainerClose{WindowID: a.cont.window, ContainerType: closeContainerType})
		a.cont = container{}
	}
	// The open-inventory action names no target entity.
	err := a.w.WritePacket(&packet.Interact{ActionType: packet.InteractActionOpenInventory})
	a.crafts.lateInventory = false
	a.mu.Unlock()
	if err != nil {
		return false
	}
	// A server that never opens the screen costs a second, not the craft.
	until := time.Now().Add(inventoryOpenIn)
	if deadline.Before(until) {
		until = deadline
	}
	for time.Now().Before(until) {
		a.mu.Lock()
		open := a.cont.open
		a.mu.Unlock()
		if open {
			return true
		}
		time.Sleep(craftPoll)
	}
	// The screen may still open after the craft gave up waiting; the read
	// loop closes it then, since nothing else would.
	a.mu.Lock()
	a.crafts.lateInventory = true
	a.mu.Unlock()
	return false
}

// planCraft picks the recipe to make and the grid its cells go in, without
// sending anything.
func (a *Actuator) planCraft(item string, times int) (craftPlan, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.book == nil {
		return craftPlan{}, ErrNoRecipe
	}
	recipes := a.book.Recipes(item)
	if len(recipes) == 0 {
		return craftPlan{}, ErrNoRecipe
	}
	inv := a.inventoryLocked()
	open := a.cont.open && a.cont.kind == protocol.ContainerTypeWorkbench
	wantsTable := false
	for _, r := range recipes {
		if !covered(inv, r.Ingredients, times) {
			continue
		}
		if r.NeedsTable() && !open {
			wantsTable = true
			continue
		}
		made := r.OutputCount * times
		if made < 1 || made > maxCraftOutput {
			return craftPlan{}, fmt.Errorf("craft of %d %s does not fit one stack", made, item)
		}
		return craftPlan{
			recipe: r,
			item:   item,
			inv:    inv,
			times:  times,
			made:   made,
			grid:   gridFor(r, open),
		}, nil
	}
	if wantsTable {
		return craftPlan{}, ErrNeedsTable
	}
	// No recipe was sent, so the only reason left is that none of them was
	// paid for.
	return craftPlan{}, ErrMissingIngredients
}

// gridFor is where a recipe's cells go: a recipe the player's 2x2 grid
// makes uses that grid, and a wider one only fits a crafting table's 3x3
// grid, which the caller has to have open.
func gridFor(r craft.Recipe, tableOpen bool) grid {
	// An open table's 3x3 grid makes the 2x2 recipes too, so it is used
	// whenever it is open rather than leaving it for the inventory screen.
	if r.NeedsTable() || tableOpen {
		return grid{base: tableGridBase, width: tableGridWidth}
	}
	return grid{base: playerGridBase, width: playerGridWidth}
}

// grid is where a recipe's cells sit in the crafting grid: base is the slot
// the first cell takes and width the number of slots a row of the grid has.
type grid struct {
	base  int
	width int
}

// slot is the grid slot a cell of the recipe takes. A shaped recipe sits at
// the top-left of the grid, so its cells are a row of the grid's width
// apart; a shapeless one fills the grid from its first slot.
func (g grid) slot(recipeWidth, cell int) int {
	if recipeWidth < 1 {
		return g.base + cell
	}
	return g.base + (cell/recipeWidth)*g.width + cell%recipeWidth
}

// fillGrid puts the recipe's ingredients into the crafting grid, one request
// per placement, and returns what the grid holds afterwards: the stack ID and
// the count of every filled cell, and how much each inventory slot gave.
func (a *Actuator) fillGrid(p craftPlan, deadline time.Time) (gridFill, error) {
	filled := gridFill{ids: map[byte]int32{}, counts: map[byte]int{}, taken: map[int]int{}, srcs: map[int]int32{}}
	for _, st := range p.inv {
		filled.srcs[st.Slot] = st.StackNetworkID
	}
	for i, cell := range p.recipe.Cells {
		if cell.Empty {
			continue
		}
		slot := byte(p.grid.slot(p.recipe.Width, i))
		need := cell.Count * p.times
		// A cell that takes more than one item may be paid for by more than
		// one stack, and the second placement merges into the cell the
		// first one filled, so the cell's stack ID is quoted back.
		dest := int32(0)
		for need > 0 {
			src, ok := sourceFor(cell, p.inv, filled.taken)
			if !ok {
				return filled, ErrMissingIngredients
			}
			n := min(need, src.Count-filled.taken[src.Slot])
			id, err := a.place(slot, src, n, dest, filled.srcs[src.Slot])
			if err != nil {
				return filled, err
			}
			if err := a.await(id, deadline); err != nil {
				// Slots and stack IDs only, never item positions in the world.
				a.mu.Lock()
				held := a.inv[src.Slot]
				a.mu.Unlock()
				a.craftLog("craft_place_refused", map[string]any{
					"inv_stack": held.StackNetworkID, "inv_count": held.Stack.Count,
					"src_slot": src.Slot, "src_stack": filled.srcs[src.Slot], "src_count": src.Count,
					"grid_slot": slot, "count": n, "error": err.Error(),
				})
				return filled, err
			}
			grid, inv := a.answeredSlots(id)
			if nid, ok := grid[slot]; ok {
				dest = nid
				filled.ids[slot] = nid
			}
			// A stack that gave up items may be a new stack now, and the
			// next placement has to quote the ID the server knows it by.
			if nid, ok := inv[byte(src.Slot)]; ok && nid != 0 {
				filled.srcs[src.Slot] = nid
			}
			filled.taken[src.Slot] += n
			if _, ok := filled.counts[slot]; !ok {
				filled.slots = append(filled.slots, slot)
			}
			filled.counts[slot] += n
			need -= n
		}
	}
	return filled, nil
}

// sourceFor picks the inventory stack that pays for a grid cell: the first
// one that matches the cell's ingredient and still has items left.
func sourceFor(cell craft.Cell, inv []belief.Item, taken map[int]int) (belief.Item, bool) {
	for _, st := range inv {
		if !cell.Matches(st.Name) {
			continue
		}
		if st.Count-taken[st.Slot] <= 0 {
			continue
		}
		return st, true
	}
	return belief.Item{}, false
}

// place moves count items from an inventory slot into a grid slot, and
// returns the request ID to await. The source stack is quoted by the ID the
// server knows it by.
func (a *Actuator) place(slot byte, src belief.Item, count int, dest, source int32) (int32, error) {
	if count < 1 || count > 255 {
		return 0, fmt.Errorf("cannot place %d items", count)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	move := &protocol.PlaceStackRequestAction{}
	move.Count = byte(count)
	move.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(src.Slot),
		StackNetworkID: source,
	}
	move.Destination = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCraftingInput},
		Slot:           slot,
		StackNetworkID: dest,
	}
	id := a.nextRequest
	if err := a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: id,
		Actions:   []protocol.StackRequestAction{move},
	}}}); err != nil {
		return 0, err
	}
	// The server accepts the negative odd IDs a client counts down through.
	a.nextRequest -= 2
	a.crafts.gridReqs[id] = true
	return id, nil
}

// sendCraft asks the server to make the recipe out of the grid's contents,
// and returns how many of the item that made.
func (a *Actuator) sendCraft(p craftPlan, filled gridFill, deadline time.Time) (int, error) {
	a.mu.Lock()
	dest, err := a.destLocked(p.item, p.inv, p.made)
	if err != nil {
		a.mu.Unlock()
		return 0, err
	}
	id := a.nextRequest
	actions := []protocol.StackRequestAction{
		&protocol.CraftRecipeStackRequestAction{
			RecipeNetworkID: p.recipe.NetworkID,
			NumberOfCrafts:  byte(p.times),
		},
		&protocol.CraftResultsDeprecatedStackRequestAction{
			ResultItems: []protocol.StackRequestItem{{
				Identifier:     "minecraft:" + p.item,
				MetadataValue:  p.recipe.Output.MetadataValue,
				BlockRuntimeID: p.recipe.Output.BlockRuntimeID,
				Count:          uint16(p.made),
			}},
			TimesCrafted: byte(p.times),
		},
	}
	// Every filled cell gives its ingredients back, and the grid's stacks
	// are the ones the answers named when the ingredients went in.
	for _, slot := range filled.slots {
		actions = append(actions, &protocol.ConsumeStackRequestAction{
			DestroyStackRequestAction: protocol.DestroyStackRequestAction{
				Count: byte(filled.counts[slot]),
				Source: protocol.StackRequestSlotInfo{
					Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCraftingInput},
					Slot:           slot,
					StackNetworkID: filled.ids[slot],
				},
			},
		})
	}
	take := &protocol.TakeStackRequestAction{}
	take.Count = byte(p.made)
	take.Source = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCreatedOutput},
		// The created stack has no ID of its own yet, and the request
		// cannot wait for the answer that would name it, so the vanilla
		// client quotes the request's own ID for it.
		Slot:           createdOutputSlot,
		StackNetworkID: id,
	}
	take.Destination = dest
	actions = append(actions, take)

	if err := a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: id,
		Actions:   actions,
	}}}); err != nil {
		a.mu.Unlock()
		return 0, err
	}
	// The server accepts the negative odd IDs a client counts down through.
	a.nextRequest -= 2
	a.crafts.pending[id] = pendingCraft{item: p.item, destSlot: int(dest.Slot)}
	// The answer names the destination's count and stack ID but never the
	// item, so the window-0 record placement reads from learns it here, or a
	// craft-then-place would not find what it just made.
	if nid, ok := a.itemIDs[p.item]; ok {
		a.reqItem[id] = nid
	}
	a.mu.Unlock()
	if err := a.await(id, deadline); err != nil {
		// A refused craft leaves its ingredients in the grid, so they go
		// back to the inventory.
		a.giveBack(p, filled)
		return 0, err
	}
	return p.made, nil
}

// giveBack takes the grid's contents back into the inventory, so a refused
// craft strands nothing. It is best effort: the craft has already failed and
// the answer is not waited for.
func (a *Actuator) giveBack(p craftPlan, filled gridFill) {
	free := freeSlots(p.inv, filled.taken)
	var actions []protocol.StackRequestAction
	for _, slot := range filled.slots {
		if len(actions) >= len(free) {
			// Nowhere to put the rest of the grid's contents.
			break
		}
		take := &protocol.TakeStackRequestAction{}
		take.Count = byte(filled.counts[slot])
		take.Source = protocol.StackRequestSlotInfo{
			Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCraftingInput},
			Slot:           slot,
			StackNetworkID: filled.ids[slot],
		}
		take.Destination = protocol.StackRequestSlotInfo{
			Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			Slot:           byte(free[len(actions)]),
			StackNetworkID: 0,
		}
		actions = append(actions, take)
	}
	if len(actions) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.nextRequest
	if err := a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: id,
		Actions:   actions,
	}}}); err != nil {
		return
	}
	a.nextRequest -= 2
	// Nobody waits for the answer, so it is dropped when it lands.
	a.abandoned[id] = true
}

// freeSlots are the inventory slots a give-back may use: the empty ones, and
// the ones the ingredients came from when they gave all they had.
func freeSlots(inv []belief.Item, taken map[int]int) []int {
	occupied := map[int]bool{}
	for _, st := range inv {
		if taken[st.Slot] < st.Count {
			occupied[st.Slot] = true
		}
	}
	var free []int
	for slot := 0; slot < inventorySlots; slot++ {
		if !occupied[slot] {
			free = append(free, slot)
		}
	}
	return free
}

// destLocked picks where the crafted items land: onto a stack of the same
// item the unit already holds, else onto the stack the last craft placed,
// else into the first empty slot. The created stack is invisible to belief
// until the unit learns it another way, so Craft keeps its own record of
// where it put it and merges there again rather than collide with it.
func (a *Actuator) destLocked(item string, inv []belief.Item, made int) (protocol.StackRequestSlotInfo, error) {
	inventory := protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory}
	occupied := make(map[int]bool, len(inv))
	for _, st := range inv {
		occupied[st.Slot] = true
		if st.Name != item || a.crafts.rejects(item, st.Slot) || st.Count+made > maxCraftOutput {
			continue
		}
		return protocol.StackRequestSlotInfo{Container: inventory, Slot: byte(st.Slot), StackNetworkID: st.StackNetworkID}, nil
	}
	if p, ok := a.crafts.placed[item]; ok && !a.crafts.rejects(item, p.slot) && p.count+made <= maxCraftOutput {
		return protocol.StackRequestSlotInfo{Container: inventory, Slot: byte(p.slot), StackNetworkID: p.stackID}, nil
	}
	for slot := 0; slot < 36; slot++ {
		if occupied[slot] || a.crafts.rejects(item, slot) {
			continue
		}
		return protocol.StackRequestSlotInfo{Container: inventory, Slot: byte(slot)}, nil
	}
	return protocol.StackRequestSlotInfo{}, errors.New("no room in the inventory for the crafted item")
}

// await polls for the server's answer outside the lock, because the read
// loop has to be able to deliver it. The craft's deadline bounds the whole
// craft, so the unit does not sit without input for too long.
func (a *Actuator) await(id int32, deadline time.Time) error {
	start := time.Now()
	limit := deadline
	if one := start.Add(craftAnswerIn); one.Before(limit) {
		limit = one
	}
	for {
		a.mu.Lock()
		status, done := a.responses[id]
		// Nothing else reads a request's answer, so it is dropped once read
		// rather than kept for the session.
		delete(a.responses, id)
		a.mu.Unlock()
		if done {
			if status != StatusOK {
				return fmt.Errorf("%w: status %d", ErrRejected, status)
			}
			return nil
		}
		if time.Now().After(limit) {
			a.mu.Lock()
			delete(a.crafts.pending, id)
			a.mu.Unlock()
			return fmt.Errorf("no answer to the stack request within %v", limit.Sub(start))
		}
		time.Sleep(craftPoll)
	}
}

// answeredSlots is what the answer to a request named: the stack IDs of the
// crafting grid's cells and of the inventory's stacks. Only the answer names
// them, and the requests that follow have to quote them.
func (a *Actuator) answeredSlots(id int32) (grid, inv map[byte]int32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	slots := a.crafts.slots[id]
	delete(a.crafts.slots, id)
	return slots.grid, slots.inv
}

func (a *Actuator) inventoryLocked() []belief.Item {
	if a.invOf == nil {
		return nil
	}
	return a.invOf()
}

// covered says whether the inventory holds every ingredient, times over.
func covered(inv []belief.Item, ingredients []craft.Ingredient, times int) bool {
	for _, ing := range ingredients {
		need, have := ing.Count*times, 0
		for _, st := range inv {
			if ing.Matches(st.Name) {
				have += st.Count
			}
		}
		if have < need {
			return false
		}
	}
	return true
}

// craftPlan is the recipe a craft makes, with the grid its cells go in, the
// inventory that pays for them and how many of the item the craft makes.
type craftPlan struct {
	recipe craft.Recipe
	item   string
	inv    []belief.Item
	times  int
	made   int
	grid   grid
}

// gridFill is what the crafting grid holds after the ingredients went in.
type gridFill struct {
	// ids is the stack ID of every grid slot the server named.
	ids map[byte]int32
	// counts is how many items each filled grid slot holds.
	counts map[byte]int
	// taken is how much each inventory slot gave the grid.
	taken map[int]int
	// srcs is the stack ID each inventory slot's stack has now, which the
	// answers name after every placement.
	srcs map[int]int32
	// slots are the grid slots that hold ingredients, in the order they
	// were filled.
	slots []byte
}

// pendingCraft is a request in flight, so its answer can be folded back
// into where the craft landed.
type pendingCraft struct {
	item     string
	destSlot int
}

// placedStack is where a craft left the item it made: belief cannot name a
// stack that appears in a slot it thought empty, so the next craft of the
// same item merges onto this record instead of colliding with it.
type placedStack struct {
	slot    int
	stackID int32
	count   int
}

// craftState is what Craft remembers between requests. It is written from the
// read loop, which folds the answers in, and read by Craft itself.
type craftState struct {
	pending map[int32]pendingCraft
	placed  map[string]placedStack
	// rejected lists, per item, the destination slots the server refused,
	// so the next craft tries another.
	rejected map[string]map[int]bool
	// slots holds, per request, the stack IDs the answer named: the
	// crafting grid's cells and the inventory's stacks.
	slots map[int32]answerSlots
	// gridReqs are the requests that filled the grid, whose answers name
	// the cells the ingredients went to.
	gridReqs map[int32]bool
	// lateInventory says a craft stopped waiting for the inventory screen
	// it asked for, so the screen is closed if it opens after all.
	lateInventory bool
}

// craftLog writes a diagnostic line about a craft, while what the server
// accepts is still being learned on the lab.
func (a *Actuator) craftLog(event string, fields map[string]any) {
	a.mu.Lock()
	log := a.correctionLogger
	a.mu.Unlock()
	if log != nil {
		log.Info(event, fields)
	}
}

// inventoryWindow is the ContainerType of the unit's own inventory screen,
// ContainerTypeInventory's -1 as the packet's byte.
const inventoryWindow = 0xff

// answerSlots is what an answer named about the stacks it moved.
type answerSlots struct {
	// grid is the crafting grid's cells.
	grid map[byte]int32
	// inv is the inventory's stacks.
	inv map[byte]int32
}

func newCraftState() craftState {
	return craftState{
		pending:  map[int32]pendingCraft{},
		placed:   map[string]placedStack{},
		rejected: map[string]map[int]bool{},
		slots:    map[int32]answerSlots{},
		gridReqs: map[int32]bool{},
	}
}

func (s *craftState) rejects(item string, slot int) bool { return s.rejected[item][slot] }

// noteLocked folds a response into the craft state: a rejection of a full
// destination rules that slot out, and an answer names the stack the craft
// placed, which only the answer knows.
func (s *craftState) noteLocked(p *packet.ItemStackResponse) {
	for _, r := range p.Responses {
		if r.Status == StatusOK {
			s.noteSlotsLocked(r)
		}
		pc, ok := s.pending[r.RequestID]
		if !ok {
			continue
		}
		delete(s.pending, r.RequestID)
		if r.Status != StatusOK {
			if r.Status == StatusOccupied {
				if s.rejected[pc.item] == nil {
					s.rejected[pc.item] = map[int]bool{}
				}
				s.rejected[pc.item][pc.destSlot] = true
			}
			continue
		}
		for _, c := range r.ContainerInfo {
			if c.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory {
				continue
			}
			for _, slot := range c.SlotInfo {
				if int(slot.Slot) == pc.destSlot && slot.Count > 0 && slot.StackNetworkID != 0 {
					s.placed[pc.item] = placedStack{slot: pc.destSlot, stackID: slot.StackNetworkID, count: int(slot.Count)}
				}
			}
		}
	}
}

// noteSlotsLocked keeps the stack IDs the answer named: the ingredients went
// into the grid one request at a time, and each answer names the stack that
// landed in the cell and the stack it came out of.
func (s *craftState) noteSlotsLocked(r protocol.ItemStackResponse) {
	if !s.gridReqs[r.RequestID] {
		return
	}
	delete(s.gridReqs, r.RequestID)
	slots := s.slots[r.RequestID]
	for _, c := range r.ContainerInfo {
		switch c.Container.ContainerID {
		case protocol.ContainerCraftingInput:
			if slots.grid == nil {
				slots.grid = map[byte]int32{}
			}
			for _, slot := range c.SlotInfo {
				slots.grid[slot.Slot] = slot.StackNetworkID
			}
		case protocol.ContainerCombinedHotBarAndInventory:
			if slots.inv == nil {
				slots.inv = map[byte]int32{}
			}
			for _, slot := range c.SlotInfo {
				slots.inv[slot.Slot] = slot.StackNetworkID
			}
		}
	}
	s.slots[r.RequestID] = slots
}
