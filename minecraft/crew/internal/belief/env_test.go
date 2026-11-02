package belief

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestPhaseBoundaries(t *testing.T) {
	for _, tc := range []struct {
		tick int64
		want string
	}{
		{0, PhaseDay}, {6000, PhaseDay}, {11999, PhaseDay},
		{12000, PhaseDusk}, {13799, PhaseDusk},
		{13800, PhaseNight}, {18000, PhaseNight}, {22199, PhaseNight},
		{22200, PhaseDawn}, {23999, PhaseDawn},
		{24000, PhaseDay}, {24000 + 12000, PhaseDusk},
		{-1, PhaseDawn}, {-24000 + 13800, PhaseNight},
	} {
		if got := PhaseOf(tc.tick); got != tc.want {
			t.Errorf("PhaseOf(%d) = %s, want %s", tc.tick, got, tc.want)
		}
	}
}

type ticking struct{ t time.Time }

func (c *ticking) now() time.Time { return c.t }

func envTracker(t *testing.T) (*Tracker, *ticking) {
	t.Helper()
	clk := &ticking{t: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	tr := New(clk.now)
	tr.Spawn(self, mgl32.Vec3{1, 2, 3}, 0, 0, 0)
	return tr, clk
}

func TestNoPhaseUntilTheServerGivesTheTime(t *testing.T) {
	tr, _ := envTracker(t)
	if got := tr.Snapshot().Env; got.DayPhase != "" || got.Raining || got.Biome != "" {
		t.Fatalf("env = %+v before any world packet", got)
	}
}

func TestTheClockAdvancesBetweenPackets(t *testing.T) {
	tr, clk := envTracker(t)
	tr.SetClock(11990, true, 0)
	if got := tr.Snapshot().Env.DayPhase; got != PhaseDay {
		t.Fatalf("phase = %s", got)
	}
	clk.t = clk.t.Add(1 * time.Second) // 20 ticks later
	if got := tr.Snapshot().Env.DayPhase; got != PhaseDusk {
		t.Errorf("phase = %s after 20 ticks, want dusk", got)
	}
	clk.t = clk.t.Add(10 * time.Minute) // a full day later
	if got := tr.Snapshot().Env.DayPhase; got == "" {
		t.Error("phase lost after a long gap")
	}

	tr.Apply(&packet.SetTime{Time: 18000})
	if got := tr.Snapshot().Env.DayPhase; got != PhaseNight {
		t.Errorf("phase = %s after a SetTime to 18000, want night", got)
	}
}

func TestAStoppedDayCycleFreezesTheClock(t *testing.T) {
	tr, clk := envTracker(t)
	tr.SetClock(5, false, 18000)
	if got := tr.Snapshot().Env.DayPhase; got != PhaseNight {
		t.Fatalf("phase = %s at the lock time 18000, want night", got)
	}
	clk.t = clk.t.Add(time.Hour)
	if got := tr.Snapshot().Env.DayPhase; got != PhaseNight {
		t.Errorf("phase = %s an hour later with the cycle stopped", got)
	}

	// Switching the cycle back on resumes from where the clock stands.
	tr.Apply(&packet.GameRulesChanged{GameRules: []protocol.GameRule{{Name: "dodaylightcycle", Value: true}}})
	clk.t = clk.t.Add(250 * time.Second) // 5000 ticks: 18000 -> 23000
	if got := tr.Snapshot().Env.DayPhase; got != PhaseDawn {
		t.Errorf("phase = %s after resuming, want dawn", got)
	}
	// And stopping it again freezes it at the current time.
	tr.Apply(&packet.GameRulesChanged{GameRules: []protocol.GameRule{{Name: "DoDaylightCycle", Value: false}}})
	clk.t = clk.t.Add(time.Hour)
	if got := tr.Snapshot().Env.DayPhase; got != PhaseDawn {
		t.Errorf("phase = %s after stopping again", got)
	}
	// A rule that is not a boolean is ignored.
	tr.Apply(&packet.GameRulesChanged{GameRules: []protocol.GameRule{{Name: "dodaylightcycle", Value: "yes"}}})
	if got := tr.Snapshot().Env.DayPhase; got != PhaseDawn {
		t.Errorf("phase = %s after a malformed rule", got)
	}
}

func TestRainStartsAndStopsOnLevelEvents(t *testing.T) {
	tr, _ := envTracker(t)
	tr.Apply(&packet.LevelEvent{EventType: packet.LevelEventStartRaining, EventData: 0})
	if !tr.Snapshot().Env.Raining {
		t.Fatal("not raining after a start with no intensity")
	}
	tr.Apply(&packet.LevelEvent{EventType: packet.LevelEventStartThunderstorm})
	if !tr.Snapshot().Env.Raining {
		t.Fatal("a thunder start stopped the rain")
	}
	tr.Apply(&packet.LevelEvent{EventType: packet.LevelEventStopRaining})
	if tr.Snapshot().Env.Raining {
		t.Fatal("still raining after the stop")
	}
}

func TestWorldPacketsDoNotMoveUpdatedAt(t *testing.T) {
	tr, clk := envTracker(t)
	before := tr.Snapshot().UpdatedAt
	clk.t = clk.t.Add(time.Minute)
	tr.Apply(&packet.LevelEvent{EventType: packet.LevelEventStartRaining})
	tr.Apply(&packet.SetTime{Time: 5})
	if got := tr.Snapshot().UpdatedAt; !got.Equal(before) {
		t.Errorf("UpdatedAt moved from %v to %v", before, got)
	}
}

// uniformBiomes is a chunk payload whose every column is one biome.
func uniformBiomes(id int32) []byte {
	return []byte{0<<1 | 1, byte(id << 1), 0}
}

func biomeDefs(names map[int16]string) *packet.BiomeDefinitionList {
	p := &packet.BiomeDefinitionList{}
	for id, name := range names {
		p.StringList = append(p.StringList, name)
		p.BiomeDefinitions = append(p.BiomeDefinitions, protocol.BiomeDefinition{NameIndex: int16(len(p.StringList) - 1), BiomeID: id})
	}
	return p
}

func TestBiomeIsTheUnitsColumnNamedFromTheServersTable(t *testing.T) {
	tr, _ := envTracker(t)
	tr.Apply(biomeDefs(map[int16]string{1: "minecraft:plains", 9: "minecraft:desert", 7: "gameops:made_up"}))
	chunk := func(x, z int32, id int32) *packet.LevelChunk {
		return &packet.LevelChunk{Position: protocol.ChunkPos{x, z}, SubChunkLimit: protocol.Option(int32(-1)), RawPayload: uniformBiomes(id)}
	}
	tr.Apply(chunk(0, 0, 1))
	tr.Apply(chunk(1, 0, 9))
	tr.Apply(chunk(2, 0, 7))

	tr.SetPose(mgl32.Vec3{3.5, 70, 4.5}, 0, 0)
	if got := tr.Snapshot().Env.Biome; got != "plains" {
		t.Errorf("biome = %q in column (0,0), want plains", got)
	}
	tr.SetPose(mgl32.Vec3{16.2, 70, 4.5}, 0, 0)
	if got := tr.Snapshot().Env.Biome; got != "desert" {
		t.Errorf("biome = %q in column (1,0), want desert", got)
	}
	tr.SetPose(mgl32.Vec3{33, 70, 4}, 0, 0)
	if got := tr.Snapshot().Env.Biome; got != BiomeOther {
		t.Errorf("biome = %q for a name off the closed list, want other", got)
	}
	tr.SetPose(mgl32.Vec3{-0.5, 70, 4}, 0, 0)
	if got := tr.Snapshot().Env.Biome; got != "" {
		t.Errorf("biome = %q in a column the unit was never sent", got)
	}
}

func TestBiomeIsUnknownWithoutTheTableOrAPosition(t *testing.T) {
	tr, _ := envTracker(t)
	tr.Apply(&packet.LevelChunk{SubChunkLimit: protocol.Option(int32(-1)), RawPayload: uniformBiomes(1)})
	tr.SetPose(mgl32.Vec3{1, 70, 1}, 0, 0)
	if got := tr.Snapshot().Env.Biome; got != "" {
		t.Errorf("biome = %q with no biome table", got)
	}
	tr.Apply(biomeDefs(map[int16]string{1: "minecraft:plains"}))
	if got := tr.Snapshot().Env.Biome; got != "plains" {
		t.Errorf("biome = %q once the table arrived", got)
	}
}

func TestChunksOfAnotherDimensionOrGarbageAreIgnored(t *testing.T) {
	tr, _ := envTracker(t)
	tr.Apply(biomeDefs(map[int16]string{1: "minecraft:plains"}))
	tr.SetPose(mgl32.Vec3{1, 70, 1}, 0, 0)
	tr.Apply(&packet.LevelChunk{Dimension: 1, SubChunkLimit: protocol.Option(int32(-1)), RawPayload: uniformBiomes(1)})
	tr.Apply(&packet.LevelChunk{SubChunkLimit: protocol.Option(int32(-1)), RawPayload: []byte{0xff, 0xff}})
	tr.Apply(&packet.LevelChunk{CacheEnabled: true, SubChunkLimit: protocol.Option(int32(-1)), RawPayload: uniformBiomes(1)})
	if got := tr.Snapshot().Env.Biome; got != "" {
		t.Errorf("biome = %q from chunks that should be ignored", got)
	}

	tr.Apply(&packet.LevelChunk{SubChunkLimit: protocol.Option(int32(-1)), RawPayload: uniformBiomes(1)})
	if got := tr.Snapshot().Env.Biome; got != "plains" {
		t.Fatalf("biome = %q", got)
	}
	tr.Apply(&packet.ChangeDimension{Dimension: 1, Position: mgl32.Vec3{1, 70, 1}})
	if got := tr.Snapshot().Env.Biome; got != "" {
		t.Errorf("biome = %q after leaving the dimension", got)
	}
}

func TestBiomeColumnsAreBounded(t *testing.T) {
	tr, _ := envTracker(t)
	for i := int32(0); i < maxBiomeColumns+500; i++ {
		tr.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{i, 0}, SubChunkLimit: protocol.Option(int32(-1)), RawPayload: uniformBiomes(1)})
	}
	tr.mu.Lock()
	n := len(tr.env.biomes)
	tr.mu.Unlock()
	if n != maxBiomeColumns {
		t.Errorf("%d columns held, want %d", n, maxBiomeColumns)
	}
}

func TestAnEndedSessionForgetsTheEnvironment(t *testing.T) {
	tr, _ := envTracker(t)
	tr.SetClock(100, true, 0)
	tr.Apply(&packet.LevelEvent{EventType: packet.LevelEventStartRaining})
	tr.Reset()
	if got := tr.Snapshot().Env; got.DayPhase != "" || got.Raining {
		t.Errorf("env = %+v after a reset", got)
	}
}

func TestClosedBiomeNames(t *testing.T) {
	for in, want := range map[string]string{"minecraft:plains": "plains", "plains": "plains", "minecraft:the_end": "the_end", "<script>": BiomeOther, "": BiomeOther} {
		if got := ClosedBiome(in); got != want {
			t.Errorf("ClosedBiome(%q) = %q, want %q", in, got, want)
		}
	}
}
