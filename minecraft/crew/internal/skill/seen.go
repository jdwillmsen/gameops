package skill

import (
	"fmt"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// ChestSeen is what a chest held when a skill closed it.
type ChestSeen struct {
	Chest protocol.BlockPos
	// Items are totals by name, without the minecraft: prefix, summed over slots.
	Items map[string]int
	At    time.Time
}

// Seer is implemented by skills that look into chests, so the runner can pass
// on the last chest they closed.
type Seer interface {
	// Seen is false until a chest has been closed.
	Seen() (ChestSeen, bool)
}

// chestItems totals the open chest's stacks by item name. The chest window is
// the only place a stack's contents are known, and it is gone once closed.
func chestItems(a Actuator, names map[int32]string) map[string]int {
	items := map[string]int{}
	for _, s := range a.ContainerContent() {
		if s.Stack.Count == 0 || s.Stack.NetworkID == 0 {
			continue
		}
		name, ok := names[s.Stack.NetworkID]
		if !ok {
			name = fmt.Sprintf("item#%d", s.Stack.NetworkID)
		}
		items[name] += int(s.Stack.Count)
	}
	return items
}

// itemNames inverts the server's item table.
func itemNames(table map[string]int32) map[int32]string {
	names := make(map[int32]string, len(table))
	for n, i := range table {
		names[i] = n
	}
	return names
}
