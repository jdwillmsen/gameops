package skill

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const (
	// Between looks at empty source chests: 5 s at 20 ticks per second.
	haulPollTicks = 100
	// Consecutive failed attempts that end a haul.
	maxRetries = 3
)

type haulPhase int

const (
	hSources haulPhase = iota
	hDeposit
	hIdle
)

// Haul empties source chests into a destination chest until each item has
// been moved as many times as wanted, waiting for items to arrive.
type Haul struct {
	world   ChestWorld
	self    func() belief.Snapshot
	sources []protocol.BlockPos
	dest    protocol.BlockPos
	want    map[string]int
	opts    []Option

	started bool
	// base is what the unit already carried of each item, which is not the
	// haul's to move.
	base  map[string]int
	moved map[string]int

	phase   haulPhase
	srcIdx  int
	queue   []string
	idle    int
	active  bool // the round took or moved something
	fails   int
	lastErr error

	sub       Skill
	subItem   string
	subBefore int
}

// NewHaul moves up to want[item] of each item from the source chests into the
// destination chest, however many trips that takes.
func NewHaul(world ChestWorld, self func() belief.Snapshot, sources []protocol.BlockPos, dest protocol.BlockPos, want map[string]int, opts ...Option) Skill {
	h := &Haul{world: world, self: self, sources: sources, dest: dest, want: map[string]int{}, opts: opts, moved: map[string]int{}}
	for k, v := range want {
		h.want[k] = v
	}
	return h
}

func (h *Haul) Name() string { return "haul" }

func (h *Haul) Ready(s belief.Snapshot) error {
	if err := overworld(s); err != nil {
		return err
	}
	if len(h.sources) == 0 || len(h.want) == 0 {
		return errors.New("nothing to haul")
	}
	return nil
}

// Progress is the total moved into the destination so far.
func (h *Haul) Progress() int {
	n := 0
	for _, v := range h.moved {
		n += v
	}
	return n
}

// Stop shuts whatever chest the running step has open.
func (h *Haul) Stop(a Actuator) {
	if st, ok := h.sub.(Stopper); ok {
		st.Stop(a)
	}
}

// Resume resumes the visit in progress; a haul between visits has nothing to
// forget.
func (h *Haul) Resume() {
	if r, ok := h.sub.(Resumer); ok {
		r.Resume()
	}
}

func (h *Haul) items() []string {
	out := make([]string, 0, len(h.want))
	for k := range h.want {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (h *Haul) carried(s belief.Snapshot, item string) int {
	return max(0, count(s, item)-h.base[item])
}

func (h *Haul) done() bool {
	for item, n := range h.want {
		if h.moved[item] < n {
			return false
		}
	}
	return true
}

func (h *Haul) stopFail(a Actuator, err error) (Status, error) {
	h.Stop(a)
	h.sub = nil
	return Failed, err
}

func (h *Haul) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return h.stopFail(a, err)
	}
	if err := overworld(h.self()); err != nil {
		return h.stopFail(a, err)
	}
	if !h.started {
		h.started, h.base = true, map[string]int{}
		for item := range h.want {
			h.base[item] = count(h.self(), item)
		}
	}
	for {
		if h.sub != nil {
			st, err := h.sub.Step(ctx, a)
			if st == Running {
				return Running, nil
			}
			if o, stop := h.endSub(st, err); stop {
				return o, h.lastErr
			}
			continue
		}
		switch h.phase {
		case hSources:
			if h.done() {
				return Done, nil
			}
			if st, err := h.nextSource(); st != Running {
				return st, err
			}
		case hDeposit:
			if st, err := h.nextDeposit(); st != Running {
				return st, err
			}
		case hIdle:
			if h.idle >= haulPollTicks {
				h.phase, h.idle = hSources, 0
				h.srcIdx, h.active = 0, false
				continue
			}
			h.idle++
			p := a.Pose()
			if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
				return Failed, err
			}
			return Running, nil
		}
	}
}

// fault records a failed attempt and says whether the haul gives up.
func (h *Haul) fault(err error) bool {
	h.lastErr = err
	if h.fails++; h.fails >= maxRetries {
		h.lastErr = fmt.Errorf("haul gave up after repeated failures: %w", err)
		return true
	}
	return false
}

// endSub books a finished sub-skill. stop is true when the haul is over, with
// the status to report.
func (h *Haul) endSub(st Status, err error) (Status, bool) {
	sub := h.sub
	h.sub = nil
	switch s := sub.(type) {
	case *Withdraw:
		for _, n := range s.Taken() {
			if n > 0 {
				h.active, h.fails = true, 0
			}
		}
		if st == Failed {
			h.srcIdx = len(h.sources)
			if h.fault(err) {
				return Failed, true
			}
		} else if s.Full() {
			h.srcIdx = len(h.sources)
		}
	default:
		if delta := h.subBefore - h.carried(h.self(), h.subItem); delta > 0 {
			h.moved[h.subItem] += delta
			h.active, h.fails = true, 0
		}
		if st == Failed && h.fault(err) {
			return Failed, true
		}
	}
	return Running, false
}

// nextSource starts a withdraw from the next source, or moves on to
// depositing once every source has been visited. It returns Running when it
// started something or changed phase.
func (h *Haul) nextSource() (Status, error) {
	snap := h.self()
	want := map[string]int{}
	for item, n := range h.want {
		if left := n - h.moved[item] - h.carried(snap, item); left > 0 {
			want[item] = left
		}
	}
	if h.srcIdx >= len(h.sources) || len(want) == 0 {
		h.phase, h.queue = hDeposit, nil
		for _, item := range h.items() {
			if h.carried(snap, item) > 0 {
				h.queue = append(h.queue, item)
			}
		}
		return Running, nil
	}
	sk := NewWithdrawChest(h.world, h.self, h.sources[h.srcIdx], want, h.opts...)
	h.srcIdx++
	return h.begin(sk, "", snap)
}

func (h *Haul) nextDeposit() (Status, error) {
	if len(h.queue) == 0 {
		h.phase = hSources
		h.srcIdx = 0
		if !h.active {
			h.phase, h.idle = hIdle, 0
		}
		h.active = false
		return Running, nil
	}
	item := h.queue[0]
	h.queue = h.queue[1:]
	snap := h.self()
	n := h.carried(snap, item)
	if n <= 0 {
		return Running, nil
	}
	return h.begin(NewDepositChest(h.world, h.self, h.dest, item, n, h.opts...), item, snap)
}

// begin starts a sub-skill, counting a failed Ready as a failed attempt.
func (h *Haul) begin(sk Skill, item string, snap belief.Snapshot) (Status, error) {
	if err := sk.Ready(snap); err != nil {
		if h.fault(err) {
			return Failed, h.lastErr
		}
		if item == "" {
			h.srcIdx = len(h.sources)
		}
		return Running, nil
	}
	h.sub, h.subItem, h.subBefore = sk, item, h.carried(snap, item)
	return Running, nil
}
