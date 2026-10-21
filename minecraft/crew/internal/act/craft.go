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
	ErrRejected           = errors.New("server rejected the craft")
)

// createdOutputSlot is the slot of ContainerCreatedOutput that the take
// reads. gophertunnel does not define it; recent vanilla clients send 50.
// It must be checked on the lab, and this constant is the one place to
// change it once it is known.
const createdOutputSlot = 50

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
	a.mu.Lock()
	id, made, err := a.craftLocked(item, times)
	a.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if err := a.await(id); err != nil {
		return 0, err
	}
	return made, nil
}

// craftLocked picks and sends the request, or decides the typed error,
// without sending anything. It must be called with the lock held.
func (a *Actuator) craftLocked(item string, times int) (int32, int, error) {
	if a.book == nil {
		return 0, 0, ErrNoRecipe
	}
	recipes := a.book.Recipes(item)
	if len(recipes) == 0 {
		return 0, 0, ErrNoRecipe
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
		return a.sendCraftLocked(r, item, inv, times)
	}
	if wantsTable {
		return 0, 0, ErrNeedsTable
	}
	// No recipe was sent, so the only reason left is that none of them was
	// paid for.
	return 0, 0, ErrMissingIngredients
}

// sendCraftLocked writes one request in the order a Bedrock client sends:
// the recipe, one consume per ingredient stack the inventory pays with, and
// the take of the created output into the inventory. It returns the request
// ID to await and how many of the item one craft makes.
func (a *Actuator) sendCraftLocked(r craft.Recipe, item string, inv []belief.Item, times int) (int32, int, error) {
	made := r.OutputCount * times
	if made < 1 || made > maxCraftOutput {
		return 0, 0, fmt.Errorf("craft of %d %s does not fit one stack", made, item)
	}
	inventory := protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory}
	actions := []protocol.StackRequestAction{&protocol.CraftRecipeStackRequestAction{
		RecipeNetworkID: r.NetworkID,
		NumberOfCrafts:  byte(times),
	}}
	// A stack can pay for two ingredients when a tag and a name both match
	// it, so what each slot has already given is tracked.
	taken := map[int]int{}
	for _, ing := range r.Ingredients {
		left := ing.Count * times
		for _, st := range inv {
			if left == 0 {
				break
			}
			if !ing.Matches(st.Name) {
				continue
			}
			n := min(left, st.Count-taken[st.Slot])
			if n <= 0 {
				continue
			}
			actions = append(actions, &protocol.ConsumeStackRequestAction{
				DestroyStackRequestAction: protocol.DestroyStackRequestAction{
					Count: byte(n),
					Source: protocol.StackRequestSlotInfo{
						Container:      inventory,
						Slot:           byte(st.Slot),
						StackNetworkID: st.StackNetworkID,
					},
				},
			})
			taken[st.Slot] += n
			left -= n
		}
		if left > 0 {
			// Unreachable while covered agrees on the same inventory; kept
			// so nothing can send a request the server would reject anyway.
			return 0, 0, ErrMissingIngredients
		}
	}
	dest, err := a.destLocked(item, inv, made)
	if err != nil {
		return 0, 0, err
	}
	take := &protocol.TakeStackRequestAction{}
	take.Count = byte(made)
	take.Source = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCreatedOutput},
		// The server assigns the created stack its ID only in the answer,
		// so the request quotes none.
		Slot: createdOutputSlot,
	}
	take.Destination = dest
	actions = append(actions, take)

	id := a.nextRequest
	if err := a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: id,
		Actions:   actions,
	}}}); err != nil {
		return 0, 0, err
	}
	// The server accepts the negative odd IDs a client counts down through.
	a.nextRequest -= 2
	a.crafts.pending[id] = pendingCraft{item: item, destSlot: int(dest.Slot)}
	// The answer names the destination's count and stack ID but never the
	// item, so the window-0 record placement reads from learns it here, or a
	// craft-then-place would not find what it just made.
	if nid, ok := a.itemIDs[item]; ok {
		a.reqItem[id] = nid
	}
	return id, made, nil
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
// loop has to be able to deliver it.
func (a *Actuator) await(id int32) error {
	deadline := time.Now().Add(craftAnswerIn)
	for {
		a.mu.Lock()
		status, done := a.responses[id]
		a.mu.Unlock()
		if done {
			if status != StatusOK {
				return fmt.Errorf("%w: status %d", ErrRejected, status)
			}
			return nil
		}
		if time.Now().After(deadline) {
			a.mu.Lock()
			delete(a.crafts.pending, id)
			a.mu.Unlock()
			return fmt.Errorf("no answer to the craft request within %v", craftAnswerIn)
		}
		time.Sleep(craftPoll)
	}
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

// craftState is what Craft remembers between requests. It is written from
// the read loop, which folds the answers in, and read by Craft itself.
type craftState struct {
	pending map[int32]pendingCraft
	placed  map[string]placedStack
	// rejected lists, per item, the destination slots the server refused,
	// so the next craft tries another.
	rejected map[string]map[int]bool
}

func newCraftState() craftState {
	return craftState{
		pending:  map[int32]pendingCraft{},
		placed:   map[string]placedStack{},
		rejected: map[string]map[int]bool{},
	}
}

func (s *craftState) rejects(item string, slot int) bool { return s.rejected[item][slot] }

// noteLocked folds a response into the craft state: a rejection of a full
// destination rules that slot out, and an answer names the stack the craft
// placed, which only the answer knows.
func (s *craftState) noteLocked(p *packet.ItemStackResponse) {
	for _, r := range p.Responses {
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
