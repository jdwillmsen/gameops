package main

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/statusapi"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/blocks"
)

type recordingConn struct {
	mu  sync.Mutex
	out []packet.Packet
}

func (c *recordingConn) WritePacket(pk packet.Packet) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out = append(c.out, pk)
	return nil
}

func (c *recordingConn) loadingScreenEnds() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, pk := range c.out {
		if l, ok := pk.(*packet.ServerBoundLoadingScreen); ok && l.Type == packet.LoadingScreenTypeEnd {
			n++
		}
	}
	return n
}

type noNames struct{}

func (noNames) BlockName(uint32) (string, bool) { return "", false }

type forever struct{}

func (forever) Name() string                { return "forever" }
func (forever) Ready(belief.Snapshot) error { return nil }
func (forever) Step(context.Context, skill.Actuator) (skill.Status, error) {
	return skill.Running, nil
}

func TestSessionWiresTasksToTheReadLoop(t *testing.T) {
	tracker := belief.New(time.Now)
	tracker.Spawn(7, mgl32.Vec3{0, 32768, 0}, 0, 0, 0)
	conn := &recordingConn{}
	runner := task.NewRunner(func(task.Template) ([]skill.Skill, error) {
		return []skill.Skill{forever{}}, nil
	}, nil, tracker.Snapshot, time.Minute, nopLog{})
	u := newUnit(7, tracker, blocks.New(noNames{}, 4), nil, runner, nil, conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)

	u.apply(&packet.ContainerOpen{WindowID: 3, ContainerPosition: protocol.BlockPos{1, 2, 3}})
	if u.actuator() != nil {
		t.Fatal("actuator built before the real position was known")
	}

	u.apply(&packet.Respawn{State: packet.RespawnStateReadyToSpawn, Position: mgl32.Vec3{4, 101.62, 5}})
	if u.actuator() == nil {
		t.Fatal("no actuator after the spawn position arrived")
	}
	if got := conn.loadingScreenEnds(); got != 1 {
		t.Fatalf("loading screen ended %d times, want once", got)
	}
	if got := u.actuator().Pose().Feet; got.Sub(mgl32.Vec3{4, 100, 5}).Len() > 0.001 {
		t.Fatalf("start pose = %v, want the feet under the believed position", got)
	}

	u.apply(&packet.ContainerOpen{WindowID: 3, ContainerPosition: protocol.BlockPos{1, 2, 3}})
	if open, id, _ := u.actuator().Container(); !open || id != 3 {
		t.Fatalf("container = %v, %d; the read loop's ContainerOpen never reached the actuator", open, id)
	}

	if _, err := runner.Submit(task.Template{Template: "gather_and_deposit"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return runner.List()[0].State == task.Running })
	u.end()
	if got := runner.List()[0]; got.State != task.Failed || got.Reason != "session ended" {
		t.Fatalf("task = %s %q, want failed: session ended", got.State, got.Reason)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition never held")
}

type nopLog struct{}

func (nopLog) Info(string, map[string]any)  {}
func (nopLog) Error(string, map[string]any) {}

func TestTaskRoutesExistOnlyWithAToken(t *testing.T) {
	tracker := belief.New(time.Now)
	runner := task.NewRunner(nil, nil, tracker.Snapshot, time.Minute, nopLog{})
	for token, want := range map[string]int{"": http.StatusNotFound, "tok": http.StatusUnauthorized} {
		rec := httptest.NewRecorder()
		statusHandler(tracker, "dev", statusapi.Unit{}, nil, runner, token).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tasks", nil))
		if rec.Code != want {
			t.Errorf("token %q: GET /v1/tasks = %d, want %d", token, rec.Code, want)
		}
	}
}

// The state route is open to anyone the gateway lets through, so it must show
// what the unit is doing without ever carrying or enabling control.
func TestStateShowsTasksButNeverTheTokenOrControl(t *testing.T) {
	const token = "s3cret-token"
	tracker := belief.New(time.Now)
	runner := task.NewRunner(func(task.Template) ([]skill.Skill, error) {
		return []skill.Skill{forever{}}, nil
	}, nil, tracker.Snapshot, time.Minute, nopLog{})
	submitted, err := runner.Submit(task.Template{Template: "gather_and_deposit", Item: "minecraft:log", Qty: 4})
	if err != nil {
		t.Fatal(err)
	}

	for _, tok := range []string{"", token} {
		h := statusHandler(tracker, "dev", statusapi.Unit{}, nil, runner, tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/state", nil))
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, submitted.ID) {
			t.Errorf("token %q: state = %d %s", tok, rec.Code, body)
		}
		if tok != "" && strings.Contains(body, tok) {
			t.Errorf("state leaks the task token: %s", body)
		}
		if strings.Contains(strings.ToLower(body), "chest") {
			t.Errorf("state leaks the task request: %s", body)
		}
	}

	h := statusHandler(tracker, "dev", statusapi.Unit{}, nil, runner, "")
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/tasks"},
		{http.MethodPost, "/v1/tasks/" + submitted.ID + "/cancel"},
		{http.MethodPost, "/v1/state"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(`{"template":"gather_and_deposit"}`)))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s with no token = %d, want 404 or 405", c.method, c.path, rec.Code)
		}
	}
	if got := runner.List()[0].State; got != task.Queued {
		t.Errorf("task state = %s; the status routes changed it", got)
	}
}

func TestUnitsOwnHotbarSelectionReachesTheTracker(t *testing.T) {
	tracker := belief.New(time.Now)
	tracker.Spawn(7, mgl32.Vec3{}, 0, 0, 0)
	conn := &recordingConn{}
	w := heldRecorder{packetWriter: conn, runtimeID: 7, tracker: tracker}

	_ = w.WritePacket(&packet.MobEquipment{EntityRuntimeID: 9, HotBarSlot: 2})
	if tracker.Snapshot().HeldKnown {
		t.Fatal("another entity's equipment moved the unit's selection")
	}
	_ = w.WritePacket(&packet.MobEquipment{EntityRuntimeID: 7, HotBarSlot: 5})
	if s := tracker.Snapshot(); !s.HeldKnown || s.HeldSlot != 5 {
		t.Errorf("held = %d known %v, want 5", s.HeldSlot, s.HeldKnown)
	}
	if len(conn.out) != 2 {
		t.Errorf("forwarded %d packets, want 2", len(conn.out))
	}
}

func TestDeathClearsTheOpenContainer(t *testing.T) {
	tracker := belief.New(time.Now)
	tracker.Spawn(7, mgl32.Vec3{}, 0, 0, 0)
	runner := task.NewRunner(nil, nil, tracker.Snapshot, time.Minute, nopLog{})
	u := newUnit(7, tracker, blocks.New(noNames{}, 4), nil, runner, nil, &recordingConn{})
	_ = u.apply(&packet.Respawn{State: packet.RespawnStateReadyToSpawn, Position: mgl32.Vec3{0, 101.62, 0}})
	_ = u.apply(&packet.ContainerOpen{WindowID: 3})
	u.died()
	if open, _, _ := u.actuator().Container(); open {
		t.Fatal("the container survived the unit's death")
	}
}

func (c *recordingConn) countInputs() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, pk := range c.out {
		if _, ok := pk.(*packet.PlayerAuthInput); ok {
			n++
		}
	}
	return n
}

func TestAnIdleUnitKeepsTicking(t *testing.T) {
	tracker := belief.New(time.Now)
	tracker.Spawn(7, mgl32.Vec3{0, 32768, 0}, 0, 0, 0)
	conn := &recordingConn{}
	world := blocks.New(noNames{}, 4)
	runner := task.NewRunner(nil, nil, tracker.Snapshot, time.Minute, nopLog{})
	lf := life.New(life.Config{Wander: true, Radius: 8, Defend: true}, world, tracker.Snapshot, nopLog{}, rand.New(rand.NewSource(1)))
	runner.SetReflex(lf)
	u := newUnit(7, tracker, world, nil, runner, lf, conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)

	u.apply(&packet.Respawn{State: packet.RespawnStateReadyToSpawn, Position: mgl32.Vec3{4, 101.62, 5}})
	waitFor(t, func() bool { return conn.countInputs() >= 5 })

}

func TestUnitsOwnWalkingReachesTheTracker(t *testing.T) {
	tracker := belief.New(time.Now)
	tracker.Spawn(7, mgl32.Vec3{}, 0, 0, 0)
	conn := &recordingConn{}
	w := heldRecorder{packetWriter: conn, runtimeID: 7, tracker: tracker}

	_ = w.WritePacket(&packet.PlayerAuthInput{Position: mgl32.Vec3{3.5, 65.62, -392.5}, Yaw: 90, Pitch: 10})
	s := tracker.Snapshot()
	if !s.PositionKnown || s.Position != [3]float32{3.5, 65.62, -392.5} || s.Yaw != 90 || s.Pitch != 10 {
		t.Errorf("snapshot = %+v, want the position the unit just sent", s)
	}
	if len(conn.out) != 1 {
		t.Errorf("forwarded %d packets, want 1", len(conn.out))
	}
}

func TestLifeConfigTurnsTheKitOnOnlyWithAChest(t *testing.T) {
	world := blocks.New(noNames{}, 1)
	lc := lifeConfig(config.Config{IdleWander: true, IdleRadius: 5, Defend: true, Eat: true, KitFood: 8}, world)
	if lc.Kit != nil || !lc.Eat || !lc.Wander || lc.Radius != 5 || !lc.Defend {
		t.Errorf("no chest: %+v", lc)
	}
	lc = lifeConfig(config.Config{KitChest: &[3]int32{1, 2, 3}, KitFood: 12}, world)
	if lc.Kit == nil || lc.Kit.Chest != (protocol.BlockPos{1, 2, 3}) || lc.Kit.Food != 12 || lc.Kit.World == nil {
		t.Errorf("with a chest: %+v", lc.Kit)
	}
}

// Only a warrior with crew-control to reach joins a squad; anything else
// keeps the reactive defence and no client.
func TestSquadClientWiresOnlyForAWarriorWithCrewControl(t *testing.T) {
	tracker := belief.New(time.Now)
	base, err := url.Parse("http://crew-control:8080/")
	if err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]config.Config{
		"neither role nor control": {},
		"no role":                  {ControlURL: base},
		"no crew-control":          {Role: "vanguard", Squad: "alpha"},
	} {
		if c := newSquadClient(cfg, tracker.Snapshot, nopLog{}); c != nil {
			t.Errorf("%s: built a squad client", name)
		}
	}
	c := newSquadClient(config.Config{Role: "striker", Squad: "bravo", ControlURL: base, ControlToken: "wt", UnitID: "crew-19"}, tracker.Snapshot, nopLog{})
	if c == nil {
		t.Fatal("a warrior with crew-control got no squad client")
	}
	// The client is the engine's chain of command, and a fresh one has
	// nothing to report from it.
	var chain life.Squad = c
	if _, _, ok := chain.Leader(); ok {
		t.Error("a fresh client already names a leader")
	}
	// The engine looks for the roster on the Squad itself.
	roster, ok := chain.(life.Roster)
	if !ok {
		t.Fatal("the squad the engine is handed is not a roster")
	}
	if n := roster.Down(); n != 0 {
		t.Errorf("a fresh client has %d squadmates down", n)
	}
	if _, ok := roster.Start(); ok {
		t.Error("a fresh client already knows a start")
	}
}

// Health unknown is not a dead heart: a straight copy of belief's zero
// health would read as a reported-death on the squad for a unit the
// server has not said anything about yet.
func TestSquadStatusDoesNotWireADeadHeartBeforeHealthIsKnown(t *testing.T) {
	tracker := belief.New(time.Now)
	tracker.Spawn(7, mgl32.Vec3{1, 65.62, 2}, 0, 0, 0)
	if s := tracker.Snapshot(); s.HealthKnown {
		t.Fatal("a spawned unit knows its health before any report")
	}
	got := squadStatus(tracker.Snapshot())
	if got.HP <= 0 {
		t.Fatalf("unknown health wired hp %g, want a value the store reads as alive", got.HP)
	}
	tracker.Apply(&packet.UpdateAttributes{EntityRuntimeID: 7, Attributes: []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: 9}},
	}})
	if got := squadStatus(tracker.Snapshot()); got.HP != 9 {
		t.Fatalf("reported health wired hp %g, want 9", got.HP)
	}
}

// The wire carries what belief holds: hp, food, the held item and where the
// unit stands — feet, under the eye position the server reports.
func TestSquadStatusCarriesBeliefOntoTheWire(t *testing.T) {
	tracker := belief.New(time.Now)
	tracker.Spawn(7, mgl32.Vec3{10, 65.62, -20}, 0, 0, 0)
	tracker.ItemNames([]protocol.ItemEntry{{Name: "minecraft:diamond_sword", RuntimeID: 303}})
	tracker.Apply(&packet.InventorySlot{WindowID: 0, Slot: 4, NewItem: protocol.ItemInstance{
		StackNetworkID: 9, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 303}, Count: 1},
	}})
	tracker.SetHeld(4)
	tracker.Apply(&packet.UpdateAttributes{EntityRuntimeID: 7, Attributes: []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: 17}},
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:player.hunger", Value: 12.7}},
	}})
	got := squadStatus(tracker.Snapshot())
	want := squadmsg.Status{HP: 17, Food: 12, Weapon: "diamond_sword", Pos: squadmsg.Pos{X: 10, Y: 64, Z: -20}}
	if got != want {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
}

// A builder role must not reach the warrior path: no life role, no squad seat.
func TestBuilderRolesStayOffTheWarriorPath(t *testing.T) {
	base, _ := url.Parse("http://crew-control.invalid")
	for _, role := range []config.Role{"mender", "builder"} {
		if got := warriorRole(role); got != "" {
			t.Errorf("warriorRole(%q) = %q, want none", role, got)
		}
		cfg := config.Config{Role: role, Lab: true, ControlURL: base, UnitID: "crew-20"}
		if c := newSquadClient(cfg, func() belief.Snapshot { return belief.Snapshot{} }, nopLog{}); c != nil {
			t.Errorf("%s got a squad client", role)
		}
	}
	if got := warriorRole("ranger"); got != "ranger" {
		t.Errorf("warriorRole(ranger) = %q", got)
	}
}

func TestCreativeInventorySentBeforeTheActuatorReachesIt(t *testing.T) {
	tracker := belief.New(time.Now)
	tracker.Spawn(7, mgl32.Vec3{}, 0, 0, 0)
	tracker.SetUniqueID(5)
	runner := task.NewRunner(nil, nil, tracker.Snapshot, time.Minute, nopLog{})
	u := newUnit(7, tracker, blocks.New(noNames{}, 4), nil, runner, nil, &recordingConn{})
	u.items = []protocol.ItemEntry{{Name: "minecraft:stone", RuntimeID: 1}, {Name: "minecraft:dirt", RuntimeID: 3}}
	_ = u.apply(&packet.CreativeContent{Items: []protocol.CreativeItem{
		{CreativeItemNetworkID: 604, Item: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 1}, Count: 1}},
	}})
	_ = u.apply(&packet.Respawn{State: packet.RespawnStateReadyToSpawn, Position: mgl32.Vec3{0, 101.62, 0}})
	_ = u.apply(&packet.UpdatePlayerGameType{GameType: 1, PlayerUniqueID: 5})
	if u.actuator() == nil {
		t.Fatal("the actuator never started")
	}
	// A full hotbar ends a take after it found the item, so the error tells
	// the table was read without the take waiting on a server.
	var slots []protocol.ItemInstance
	for i := 0; i < 9; i++ {
		slots = append(slots, protocol.ItemInstance{StackNetworkID: int32(i + 1), Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 3}, Count: 1}})
	}
	_ = u.apply(&packet.InventoryContent{WindowID: 0, Content: slots})
	if err := u.actuator().TakeCreative("dirt"); !errors.Is(err, act.ErrNoCreativeItem) {
		t.Errorf("dirt: err = %v, want ErrNoCreativeItem", err)
	}
	if err := u.actuator().TakeCreative("stone"); !errors.Is(err, act.ErrHotbarFull) {
		t.Errorf("stone: err = %v, want ErrHotbarFull: the table or the mode never reached the actuator", err)
	}
}
