package skill

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const creativeWindow byte = 9

// creativeIDs are the creative-inventory IDs of the items the rig's server
// can place; they differ from the item IDs, as on the wire.
var creativeIDs = map[int32]uint32{idDirt: 700, idCobblestone: 701}

// goCreative puts the unit in creative mode on a server that opens the
// inventory screen and answers creative takes. The answers come from a
// goroutine because the actuator waits for them with its lock released.
func (r *rig) goCreative() {
	r.t.Helper()
	const unitID = 5
	r.tracker.SetUniqueID(unitID)
	r.tracker.Apply(&packet.UpdatePlayerGameType{GameType: 1, PlayerUniqueID: unitID})
	r.Actuator.SetCreative(r.tracker.Creative)
	var items []protocol.CreativeItem
	for item, cid := range creativeIDs {
		items = append(items, protocol.CreativeItem{CreativeItemNetworkID: cid, Item: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: item}, Count: 1}})
	}
	r.Actuator.Apply(&packet.CreativeContent{Items: items})

	in := make(chan packet.Packet, 8)
	done := make(chan struct{})
	r.onWrite = func(pk packet.Packet) bool {
		switch p := pk.(type) {
		case *packet.Interact:
			in <- p
			return true
		case *packet.ItemStackRequest:
			if _, ok := p.Requests[0].Actions[0].(*protocol.CraftCreativeStackRequestAction); ok {
				in <- p
				return true
			}
		}
		return false
	}
	go func() {
		defer close(done)
		stackID := int32(900)
		for pk := range in {
			switch p := pk.(type) {
			case *packet.Interact:
				r.Actuator.Apply(&packet.ContainerOpen{WindowID: creativeWindow, ContainerType: 0xff, ContainerEntityUniqueID: -1})
			case *packet.ItemStackRequest:
				req := p.Requests[0]
				craft := req.Actions[0].(*protocol.CraftCreativeStackRequestAction)
				take := req.Actions[1].(*protocol.TakeStackRequestAction)
				for item, cid := range creativeIDs {
					if cid == craft.CreativeItemNetworkID {
						stackID++
						r.inv[take.Destination.Slot] = slotStack{item, int(take.Count), stackID}
					}
				}
				r.Actuator.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{
					RequestID: req.RequestID,
					ContainerInfo: []protocol.StackResponseContainerInfo{{
						Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
						SlotInfo:  []protocol.StackResponseSlotInfo{{Slot: take.Destination.Slot, HotbarSlot: take.Destination.Slot, Count: take.Count, StackNetworkID: stackID}},
					}},
				}}})
			}
		}
	}()
	r.t.Cleanup(func() {
		close(in)
		<-done
	})
}

// stateWorld gives the rig's block map the states a unit's store has, none.
type stateWorld struct{ simWorld }

func (w stateWorld) State(p protocol.BlockPos) (string, map[string]any, bool) {
	n, ok := w.simWorld[p]
	return n, nil, ok
}
