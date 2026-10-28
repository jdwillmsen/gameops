package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/liveness"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcdial"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcproto"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/camera"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/blocks"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape/blocktable"
)

const (
	eyeHeight   = 1.62
	tickEvery   = 50 * time.Millisecond
	chunkRadius = 4
)

// unit is one joined client: the same connection, belief, block store and
// actuator a crew unit has, driven by calibration instead of a skill.
type unit struct {
	name    string
	conn    *minecraft.Conn
	leave   func(time.Duration)
	tracker *belief.Tracker
	store   *blocks.Store
	dec     *camera.NetworkDecoder
	gd      minecraft.GameData
	proto   minecraft.Protocol

	mu       sync.Mutex
	act      *act.Actuator
	creative *packet.CreativeContent
	look     lookIntent
	brk      *act.BreakAction
	err      error
	ended    chan struct{}
	// tap, when set, sees every packet the read loop gets; probes use it to
	// catch item drops and similar events that are not blocks.
	tap func(packet.Packet)
}

type lookIntent struct {
	yaw, pitch float32
	sneak      bool
}

// unmappedHashes counts, across units, hashes the registry did not know.
var unmapped struct {
	mu sync.Mutex
	n  map[uint32]bool
}

func noteUnmapped(h uint32) {
	unmapped.mu.Lock()
	defer unmapped.mu.Unlock()
	if unmapped.n == nil {
		unmapped.n = map[uint32]bool{}
	}
	unmapped.n[h] = true
}

func unmappedCount() int {
	unmapped.mu.Lock()
	defer unmapped.mu.Unlock()
	return len(unmapped.n)
}

// join dials the lab as an offline player and starts the read and tick loops.
func join(ctx context.Context, addr, name string, log io.Writer) (*unit, error) {
	shape.Fallback = blocktable.Boxes
	proto := mcproto.Negotiate(ctx, addr, nil)
	conn, leave, err := mcdial.DialWithLeave(ctx, minecraft.Dialer{
		IdentityData:         login.IdentityData{DisplayName: name},
		Protocol:             proto,
		ErrorLog:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		DownloadResourcePack: func(uuid.UUID, string, int, int) bool { return false },
	}, addr, 30*time.Second, 90*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", name, err)
	}
	if err := conn.DoSpawnContext(ctx); err != nil {
		leave(2 * time.Second)
		return nil, fmt.Errorf("spawn %s: %w", name, err)
	}
	gd := conn.GameData()
	tracker := belief.New(time.Now)
	tracker.Spawn(gd.EntityRuntimeID, gd.PlayerPosition, gd.Yaw, gd.Pitch, gd.Dimension)
	tracker.SetUniqueID(gd.EntityUniqueID)
	tracker.ItemNames(gd.Items)
	dec := camera.NewNetworkDecoder(func(string) {})
	dec.BeginSession("")
	u := &unit{
		name: name, conn: conn, leave: leave, tracker: tracker, dec: dec, gd: gd, proto: proto,
		store: blocks.New(dec, 6000), ended: make(chan struct{}),
	}
	if err := conn.WritePacket(&packet.RequestChunkRadius{ChunkRadius: chunkRadius, MaxChunkRadius: chunkRadius}); err != nil {
		leave(2 * time.Second)
		return nil, err
	}
	go u.readLoop()
	go u.tickLoop()
	return u, nil
}

func (u *unit) actuator() *act.Actuator {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.act
}

func (u *unit) readLoop() {
	defer close(u.ended)
	respawner := liveness.New(u.gd.EntityRuntimeID)
	for {
		pk, err := u.conn.ReadPacket()
		if err != nil {
			u.mu.Lock()
			u.err = err
			u.mu.Unlock()
			return
		}
		_, _ = respawner.Handle(pk, u.conn)
		u.mu.Lock()
		tap := u.tap
		u.mu.Unlock()
		if tap != nil {
			tap(pk)
		}
		if cc, ok := pk.(*packet.CreativeContent); ok {
			u.mu.Lock()
			u.creative = cc
			u.mu.Unlock()
		}
		u.tracker.Apply(pk)
		u.store.Apply(pk, u.conn)
		if a := u.actuator(); a != nil {
			a.Apply(pk)
			continue
		}
		snap := u.tracker.Snapshot()
		if !snap.PositionKnown {
			continue
		}
		a := act.New(u.conn, u.gd.EntityRuntimeID, act.Pose{
			Feet:     mgl32.Vec3{snap.Position[0], snap.Position[1] - eyeHeight, snap.Position[2]},
			Yaw:      snap.Yaw,
			Pitch:    snap.Pitch,
			OnGround: true,
		}, path.NewSolid(u.store))
		a.ItemNames(u.gd.Items)
		a.SetCreative(u.tracker.Creative)
		u.mu.Lock()
		cc := u.creative
		u.mu.Unlock()
		if cc != nil {
			a.Apply(cc)
		}
		if err := a.EndLoadingScreen(); err != nil {
			u.mu.Lock()
			u.err = err
			u.mu.Unlock()
			return
		}
		u.mu.Lock()
		u.act = a
		u.mu.Unlock()
	}
}

// tickLoop sends one input every tick, as a real client does, carrying the
// look and sneak the trial set and at most one queued block break.
func (u *unit) tickLoop() {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-u.ended:
			return
		case <-t.C:
		}
		a := u.actuator()
		if a == nil {
			continue
		}
		u.mu.Lock()
		in := act.Input{Yaw: u.look.yaw, Pitch: u.look.pitch, Sneak: u.look.sneak, Break: u.brk}
		u.brk = nil
		u.mu.Unlock()
		if err := a.Tick(in); err != nil {
			return
		}
	}
}

// alive reports whether the connection still reads.
func (u *unit) alive() bool {
	select {
	case <-u.ended:
		return false
	default:
		return true
	}
}

func (u *unit) failure() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.err != nil {
		return u.err
	}
	return errors.New("connection ended")
}

func (u *unit) close() { u.leave(3 * time.Second) }

// waitFor polls cond until it holds, the unit drops, or the timeout passes.
func (u *unit) waitFor(ctx context.Context, timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if ctx.Err() != nil || !u.alive() || !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ready waits until the actuator exists.
func (u *unit) ready(ctx context.Context, timeout time.Duration) bool {
	return u.waitFor(ctx, timeout, func() bool { return u.actuator() != nil })
}

// setLook sets the look and sneak the following inputs carry and waits until
// one input carrying them has been sent, so a click right after is read by
// the server with that look.
func (u *unit) setLook(ctx context.Context, c click) {
	a := u.actuator()
	if a == nil {
		return
	}
	u.mu.Lock()
	changed := u.look.sneak != c.Sneak
	u.look = lookIntent{yaw: float32(c.Yaw), pitch: float32(c.Pitch), sneak: c.Sneak}
	u.mu.Unlock()
	wait := uint64(1)
	if changed {
		wait = 4
	}
	before := a.Ticks()
	u.waitFor(ctx, time.Second, func() bool { return a.Ticks() >= before+wait })
}

func (u *unit) hash(p protocol.BlockPos) (uint32, bool) { return u.store.BlockHash(p) }

func (u *unit) isAir(p protocol.BlockPos) bool {
	h, ok := u.hash(p)
	return ok && h == airHash
}

// read is the block at p, or placed=false when it is air or not known.
func (u *unit) read(p protocol.BlockPos) outcome {
	h, ok := u.hash(p)
	if !ok || h == airHash {
		return outcome{}
	}
	name, props, known := u.store.State(p)
	if !known {
		noteUnmapped(h)
		return outcome{Placed: true, Unmapped: true, Name: fmt.Sprintf("unmapped:%d", h)}
	}
	return outcome{Placed: true, Name: name, Props: props}
}

// breakAt breaks the block at p with the survival start-break action, which
// a creative unit's server answers at once, and waits for air. It tries
// again, because an input can be dropped around a teleport or a correction.
func (u *unit) breakAt(ctx context.Context, p protocol.BlockPos) bool {
	for try := 0; try < 3; try++ {
		if u.isAir(p) {
			return true
		}
		u.mu.Lock()
		u.brk = &act.BreakAction{Pos: p, Face: 1, Phase: act.BreakStart}
		u.mu.Unlock()
		if u.waitFor(ctx, 1200*time.Millisecond, func() bool { return u.isAir(p) }) {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
	}
	return false
}

// teleportedTo waits until the unit's own pose has moved to near the point.
func (u *unit) teleportedTo(ctx context.Context, p mgl32.Vec3, timeout time.Duration) bool {
	a := u.actuator()
	return a != nil && u.waitFor(ctx, timeout, func() bool { return a.Pose().Feet.Sub(p).Len() < 1.5 })
}

// click sends the held item at the face of the block against.
func (u *unit) click(ctx context.Context, against protocol.BlockPos, f face, hit [3]float32, item string) error {
	a := u.actuator()
	if a == nil {
		return errors.New("no actuator")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := a.ClickBlock(against, int32(f), mgl32.Vec3(hit), item)
		if !errors.Is(err, act.ErrItemMoving) || ctx.Err() != nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// hotbarUsed counts hotbar slots holding something.
func (u *unit) hotbarUsed() int {
	n := 0
	for _, it := range u.tracker.Snapshot().Inventory {
		if it.Slot < 9 && it.Count > 0 {
			n++
		}
	}
	return n
}

// heldItems names what the hotbar holds.
func (u *unit) heldItems() map[string]bool {
	out := map[string]bool{}
	for _, it := range u.tracker.Snapshot().Inventory {
		if it.Slot < 9 && it.Count > 0 {
			out[it.Name] = true
		}
	}
	return out
}
