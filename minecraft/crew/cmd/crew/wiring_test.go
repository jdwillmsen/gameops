package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
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
	u := newUnit(7, tracker, blocks.New(noNames{}, 4), nil, runner, conn)

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

func (nopLog) Info(string, map[string]any) {}

func TestTaskRoutesExistOnlyWithAToken(t *testing.T) {
	tracker := belief.New(time.Now)
	runner := task.NewRunner(nil, nil, tracker.Snapshot, time.Minute, nopLog{})
	for token, want := range map[string]int{"": http.StatusNotFound, "tok": http.StatusUnauthorized} {
		rec := httptest.NewRecorder()
		statusHandler(tracker, "dev", nil, runner, token).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tasks", nil))
		if rec.Code != want {
			t.Errorf("token %q: GET /v1/tasks = %d, want %d", token, rec.Code, want)
		}
	}
}
