package structures

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/leveldat"
)

var (
	metricRecorded = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_structures_recorded",
		Help: "Structures the world has recorded, by dimension and kind, at the last survey.",
	}, []string{"dimension", "kind"})
	metricPredicted = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_structures_predicted",
		Help: "Structures predicted from the seed and offered to the page, by dimension and kind. Zero while the seed is not verified.",
	}, []string{"dimension", "kind"})
	metricSeedVerified = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_seed_verified",
		Help: "1 while the structures the world recorded are where the seed says they would be, which is what lets predictions be shown.",
	})
	metricDisagreements = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_prediction_disagreements",
		Help: "Places where the seed and the world's records disagree: a recorded structure no site explains, or a site in a finished chunk with nothing recorded.",
	})
	metricSkipped = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_structures_areas_skipped",
		Help: "Recorded areas left out of the last survey, by reason: malformed, unknown (a kind this version does not know), or limit.",
	}, []string{"reason"})
	metricSurveyAt = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_survey_last_success_timestamp_seconds",
		Help: "When the world's structures were last read.",
	})
	metricSurveySeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_survey_duration_seconds",
		Help: "How long the last successful survey took.",
	})
	metricSurveyFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_structures_survey_failures_total",
		Help: "Surveys that could not read the world.",
	})
	metricVillagesSkipped = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_structures_villages_skipped",
		Help: "Villages left out of the last survey, by reason: empty (counted by the game, no villagers), malformed, unknown (a key this version does not know), or limit.",
	}, []string{"reason"})
	metricVillageSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_village_read_duration_seconds",
		Help: "How long the last successful read of the village records took.",
	})
	metricVillagesAt = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_villages_last_success_timestamp_seconds",
		Help: "The snapshot the villages being served were read from. It falls behind the survey's while the village records cannot be read.",
	})
	metricVillageFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_structures_village_read_failures_total",
		Help: "Surveys that could not read the village records in time and kept the villages of the one before.",
	})
)

// Bounds on one survey. The FWB world holds 1,274 areas in 27 structures;
// these are what a damaged or hostile world is held to.
const (
	maxAreas = 200_000
	// MaxPerLayer is the most structures of each sort, recorded and
	// predicted, kept for one dimension, and so the most a browser is sent.
	MaxPerLayer = 2000
	// predictionMargin is how far past the generated world predictions
	// reach, in chunks: the country a player could walk into next.
	predictionMargin = 64
	// minEvidence is how many recorded structures have to be where the
	// seed puts them before the seed is believed. One could be chance, at
	// about one in six hundred; three could not.
	minEvidence = 3
	// maxReach is how far from the middle of the generated world, in
	// chunks, structures are predicted and checked: 49,000 blocks each way.
	// A dimension's extent comes from its chunk keys, so one stray chunk a
	// million blocks out would otherwise stretch the area over everything
	// in between, and a region walk cut short there would look at the
	// empty end of it.
	maxReach = 3072
	// maxSites is how many sites of one kind are set beside the world. It
	// is apart from the layer's bound because the seed is checked against
	// every one of them, shown or not.
	maxSites    = 20_000
	maxFindings = 50
	// villageTimeout is how long the village records may take to read.
	// They are under one prefix and take milliseconds; this is what keeps
	// a world that has made them enormous from holding up the cycle.
	villageTimeout = 10 * time.Second
)

// How far a seed has been checked against the world.
const (
	// SeedVerified: recorded structures are where the seed puts them.
	SeedVerified = "verified"
	// SeedUnverified: the world has recorded too few structures to tell.
	SeedUnverified = "unverified"
	// SeedRefuted: recorded structures are not where the seed puts them, so
	// either it is not the seed the world generates from or the placement
	// rules here are wrong for this version of the game.
	SeedRefuted = "refuted"
	// SeedUnknown: no seed could be read.
	SeedUnknown = "unknown"
)

// Prediction is a structure the seed says the generator builds.
type Prediction struct {
	Kind Kind  `json:"kind"`
	X    int32 `json:"x"`
	Z    int32 `json:"z"`
	// Generated is set where the site's chunk is already complete and the
	// world recorded nothing there: the prediction and the world disagree.
	Generated bool `json:"generated,omitempty"`
}

// Layer is one dimension's structures.
type Layer struct {
	Recorded      []Structure
	RecordedMore  int
	Predicted     []Prediction
	PredictedMore int
}

// Check is the result of comparing the seed's sites with the world.
type Check struct {
	State string
	// Agree and Disagree count recorded structures of the kinds whose
	// placement is exact: those a site explains, and those none does.
	Agree, Disagree int
	// Findings describes each disagreement, of any kind, up to a limit.
	Findings []string
	// Total is how many disagreements there are, described or not.
	Total int
}

// Survey is what one reading of the world found.
type Survey struct {
	At     time.Time
	Layers map[chunks.Dimension]Layer
	Check  Check
	// Level is the world's settings, when level.dat could be read. The
	// seed in it is for this service's own calculations and is not served.
	Level    leveldat.Level
	HasLevel bool
	// StructureSeed is the 32 bits structure placement was worked out
	// from, when there were any; Check says whether the world bears them
	// out. It is not served either.
	StructureSeed    uint32
	HasStructureSeed bool
	// Areas is how many recorded areas were read; the rest were left out.
	Areas, Malformed, Unknown, OverLimit int
	// Villages is what the village records came to.
	Villages VillageStats

	// Every village found, before any layer's limit, for the next survey
	// to fall back on.
	villages map[chunks.Dimension][]Structure
}

// Surveyor reads the world's structures once per snapshot and keeps the
// result for the page.
type Surveyor struct {
	// WorkDir is where a survey builds its view of the world; on the same
	// filesystem as the mirror, and used by nothing else.
	WorkDir string
	// Predictors are the kinds predicted from the seed. None, and only
	// recorded structures are found.
	Predictors []Predictor
	// StructureSeed, when set, is used in place of the low half of the
	// seed in level.dat. It is checked against the world the same way.
	StructureSeed *uint32
	// VillageTimeout bounds the read of the village records within a
	// survey; zero means ten seconds.
	VillageTimeout time.Duration
	Logger         *slog.Logger

	// Limits, for tests; zero means maxAreas, MaxPerLayer and maxVillages.
	areaLimit, layerLimit, villageLimit int

	mu       sync.Mutex
	last     Survey
	surveyed bool
	// What was last logged, so that a finding is reported when it appears
	// and not again every cycle while it stands.
	logged []string
}

// Last is the most recent survey, if there has been one.
func (s *Surveyor) Last() (Survey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last, s.surveyed
}

// Take reads the structures of the world in worldDir, which holds level.dat
// and the db directory, as of at. A failure leaves the last survey in place.
func (s *Surveyor) Take(ctx context.Context, worldDir string, at time.Time) (Survey, error) {
	started := time.Now()
	survey, err := s.take(ctx, worldDir, at)
	if err != nil {
		metricSurveyFailures.Inc()
		return Survey{}, err
	}
	s.mu.Lock()
	s.last, s.surveyed = survey, true
	changed := !slices.Equal(s.logged, survey.Check.Findings)
	s.logged = survey.Check.Findings
	s.mu.Unlock()

	export(survey)
	if v := survey.Villages; !v.Stale {
		metricVillagesAt.Set(float64(at.Unix()))
		s.Logger.Info("villages read", "found", v.Found, "empty", v.Empty, "malformed", v.Malformed, "unknown", v.Unknown, "over_limit", v.OverLimit)
	}
	metricSurveyAt.Set(float64(at.Unix()))
	metricSurveySeconds.Set(time.Since(started).Seconds())
	switch {
	case !changed:
	case survey.Check.State == SeedRefuted:
		// Every recorded structure is a finding then, and one line says
		// what all of them mean.
		s.Logger.Warn("the seed does not put this world's recorded structures where they are, so nothing is predicted; if the world generates from another seed, STRUCTURE_SEED supplies it",
			"agree", survey.Check.Agree, "disagree", survey.Check.Disagree)
	default:
		for _, finding := range survey.Check.Findings {
			s.Logger.Warn("structure prediction disagrees with the world", "finding", finding)
		}
	}
	return survey, nil
}

func (s *Surveyor) take(ctx context.Context, worldDir string, at time.Time) (Survey, error) {
	areaLimit, layerLimit := s.areaLimit, s.layerLimit
	if areaLimit == 0 {
		areaLimit = maxAreas
	}
	if layerLimit == 0 {
		layerLimit = MaxPerLayer
	}
	survey := Survey{At: at, Layers: map[chunks.Dimension]Layer{}, Check: Check{State: SeedUnknown}}
	db, release, err := chunks.OpenView(filepath.Join(worldDir, "db"), s.WorkDir)
	if err != nil {
		return Survey{}, err
	}
	defer release()

	pieces := map[chunks.Dimension][]piece{}
	extents := map[chunks.Dimension]*extent{}
	it := db.NewIterator(nil, nil)
	defer it.Release()
	for n := 0; it.Next(); n++ {
		if n%65536 == 0 && ctx.Err() != nil {
			return Survey{}, ctx.Err()
		}
		pos, tag, ok := chunks.RecordOf(it.Key())
		if !ok {
			continue
		}
		switch tag {
		case TagFinalized:
			e, seen := extents[pos.Dim]
			if !seen {
				e = &extent{Area: Area{pos.X, pos.Z, pos.X, pos.Z}}
				extents[pos.Dim] = e
			}
			e.add(pos)
		case TagSpawnAreas:
			found, unknown, malformed, err := decode(pos, it.Value())
			if err != nil {
				survey.Malformed++
				continue
			}
			survey.Unknown += unknown
			survey.Malformed += malformed
			if room := areaLimit - survey.Areas; len(found) > room {
				survey.OverLimit += len(found) - room
				found = found[:room]
			}
			survey.Areas += len(found)
			pieces[pos.Dim] = append(pieces[pos.Dim], found...)
		}
	}
	if err := it.Error(); err != nil {
		return Survey{}, fmt.Errorf("read world: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Survey{}, err
	}

	recorded := map[chunks.Dimension][]Structure{}
	for _, d := range chunks.Dimensions {
		recorded[d] = assemble(pieces[d])
	}
	if survey.villages, survey.Villages, err = s.readVillages(ctx, db); err != nil {
		return Survey{}, err
	}

	predicted := map[chunks.Dimension][]Prediction{}
	more := map[chunks.Dimension]int{}
	seed, haveSeed := s.seed(worldDir, &survey)
	survey.StructureSeed, survey.HasStructureSeed = seed, haveSeed
	if haveSeed && len(s.Predictors) > 0 {
		finished := func(p chunks.Pos) (bool, error) {
			v, err := db.Get(chunks.RecordKey(p, TagFinalized), nil)
			if errors.Is(err, leveldb.ErrNotFound) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return len(v) >= 4 && binary.LittleEndian.Uint32(v) == finalizedDone, nil
		}
		var err error
		if survey.Check, predicted, more, err = compare(s.Predictors, seed, layerLimit, recorded, extents, finished); err != nil {
			return Survey{}, fmt.Errorf("read world: %w", err)
		}
	}

	for _, d := range chunks.Dimensions {
		// Villages come after the structures the seed is checked against,
		// and are the first to go where a layer is cut short.
		layer := Layer{Recorded: append(slices.Clip(recorded[d]), survey.villages[d]...)}
		if len(layer.Recorded) > layerLimit {
			layer.RecordedMore = len(layer.Recorded) - layerLimit
			layer.Recorded = layer.Recorded[:layerLimit]
		}
		// A prediction is only offered while the seed it came from has
		// been seen to put this world's own structures where they are.
		if survey.Check.State == SeedVerified {
			layer.Predicted, layer.PredictedMore = predicted[d], more[d]
		}
		survey.Layers[d] = layer
	}
	return survey, nil
}

// readVillages reads the village records within their own time. Running out
// of it, or failing to read them, is not the survey's failure: the villages
// of the last survey stand, and everything else is as fresh as it would be.
func (s *Surveyor) readVillages(ctx context.Context, db *leveldb.DB) (map[chunks.Dimension][]Structure, VillageStats, error) {
	limit, timeout := s.villageLimit, s.VillageTimeout
	if limit == 0 {
		limit = maxVillages
	}
	if timeout == 0 {
		timeout = villageTimeout
	}
	started := time.Now()
	within, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	found, stats, err := readVillages(within, db, limit)
	if err == nil {
		metricVillageSeconds.Set(time.Since(started).Seconds())
		return found, stats, nil
	}
	if ctx.Err() != nil {
		// The survey itself was stopped, which is its failure to report.
		return nil, VillageStats{}, ctx.Err()
	}
	metricVillageFailures.Inc()
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	stats = last.Villages
	stats.Stale = true
	s.Logger.Error("villages not read; those of the last survey are kept", "error", err, "kept", stats.Found)
	return last.villages, stats, nil
}

// seed is the 32 bits structure placement is seeded with. The game uses the
// low half of the world seed, which is what this takes from level.dat
// unless the operator has supplied another.
func (s *Surveyor) seed(worldDir string, survey *Survey) (uint32, bool) {
	level, err := leveldat.Read(filepath.Join(worldDir, "level.dat"))
	if err != nil {
		// Recorded structures need no seed, so they are still served.
		s.Logger.Warn("level.dat not read; nothing is predicted this cycle", "error", err)
	} else {
		survey.Level, survey.HasLevel = level, true
	}
	if s.StructureSeed != nil {
		return *s.StructureSeed, true
	}
	return uint32(level.Seed), err == nil
}

// extent is where a dimension's chunks are: the box around all of them,
// and every chunk's position, which is eight bytes a chunk and is what the
// middle of the world is taken from.
type extent struct {
	Area
	xs, zs []int32
}

func (e *extent) add(p chunks.Pos) {
	e.Area = Area{min(e.MinX, p.X), min(e.MinZ, p.Z), max(e.MaxX, p.X), max(e.MaxZ, p.Z)}
	e.xs = append(e.xs, p.X)
	e.zs = append(e.zs, p.Z)
}

// window is the area structures are predicted and checked in: the extent
// and a margin, kept within maxReach of the middle of the chunks. The
// middle is the median on each axis, which no number of far-off chunks
// short of half the world can move.
func (e *extent) window() Area {
	slices.Sort(e.xs)
	slices.Sort(e.zs)
	midX, midZ := e.xs[len(e.xs)/2], e.zs[len(e.zs)/2]
	return Area{
		max(e.MinX-predictionMargin, midX-maxReach), max(e.MinZ-predictionMargin, midZ-maxReach),
		min(e.MaxX+predictionMargin, midX+maxReach), min(e.MaxZ+predictionMargin, midZ+maxReach),
	}
}

func (a Area) holds(b Box) bool {
	return b.MinX>>4 >= a.MinX && b.MaxX>>4 <= a.MaxX && b.MinZ>>4 >= a.MinZ && b.MaxZ>>4 <= a.MaxZ
}

// compare sets each predictor's sites beside what the world recorded.
func compare(
	predictors []Predictor,
	seed uint32,
	limit int,
	recorded map[chunks.Dimension][]Structure,
	extents map[chunks.Dimension]*extent,
	finished func(chunks.Pos) (bool, error),
) (Check, map[chunks.Dimension][]Prediction, map[chunks.Dimension]int, error) {
	check := Check{}
	predicted := map[chunks.Dimension][]Prediction{}
	more := map[chunks.Dimension]int{}
	find := func(format string, args ...any) {
		check.Total++
		if len(check.Findings) < maxFindings {
			check.Findings = append(check.Findings, fmt.Sprintf(format, args...))
		}
	}
	for _, p := range predictors {
		d := p.Dimension()
		generated, ok := extents[d]
		if !ok {
			continue
		}
		area := generated.window()
		sites, left := p.Sites(seed, area, maxSites)
		if p.Certain() {
			more[d] += left
		}

		explained := map[int]bool{}
		for _, site := range sites {
			built := false
			for i, real := range recorded[d] {
				if real.Kind == p.Kind() && p.Explains(site, real.Box) {
					explained[i], built = true, true
				}
			}
			if built || !p.Certain() {
				continue
			}
			done, err := finished(chunks.Pos{Dim: d, X: site.ChunkX, Z: site.ChunkZ})
			if err != nil {
				return Check{}, nil, nil, err
			}
			x, z := p.Centre(site)
			if done {
				find("%s predicted at %s %d, %d: that chunk is generated and the world recorded none", p.Kind(), d.Name(), x, z)
			}
			if len(predicted[d]) >= limit {
				more[d]++
				continue
			}
			predicted[d] = append(predicted[d], Prediction{Kind: p.Kind(), X: x, Z: z, Generated: done})
		}
		for i, real := range recorded[d] {
			// A structure outside the window had no site looked for, so it
			// says nothing about the seed either way.
			if real.Kind != p.Kind() || !area.holds(real.Box) {
				continue
			}
			switch {
			case explained[i] && p.Exact():
				check.Agree++
			case !explained[i]:
				if p.Exact() {
					check.Disagree++
				}
				find("%s recorded at %s %d, %d to %d, %d: the seed puts none there", real.Kind, d.Name(), real.MinX, real.MinZ, real.MaxX, real.MaxZ)
			}
		}
	}
	switch {
	case check.Agree >= minEvidence && check.Agree > check.Disagree:
		check.State = SeedVerified
	case check.Agree+check.Disagree >= minEvidence:
		check.State = SeedRefuted
	default:
		check.State = SeedUnverified
	}
	return check, predicted, more, nil
}

func export(survey Survey) {
	for _, d := range chunks.Dimensions {
		recorded, predicted := map[Kind]int{}, map[Kind]int{}
		for _, r := range survey.Layers[d].Recorded {
			recorded[r.Kind]++
		}
		for _, p := range survey.Layers[d].Predicted {
			predicted[p.Kind]++
		}
		for _, kind := range Kinds {
			metricRecorded.WithLabelValues(d.Name(), string(kind)).Set(float64(recorded[kind]))
			metricPredicted.WithLabelValues(d.Name(), string(kind)).Set(float64(predicted[kind]))
		}
	}
	verified := 0.0
	if survey.Check.State == SeedVerified {
		verified = 1
	}
	metricSeedVerified.Set(verified)
	metricDisagreements.Set(float64(survey.Check.Total))
	metricSkipped.WithLabelValues("malformed").Set(float64(survey.Malformed))
	metricSkipped.WithLabelValues("unknown").Set(float64(survey.Unknown))
	metricSkipped.WithLabelValues("limit").Set(float64(survey.OverLimit))
	metricVillagesSkipped.WithLabelValues("empty").Set(float64(survey.Villages.Empty))
	metricVillagesSkipped.WithLabelValues("malformed").Set(float64(survey.Villages.Malformed))
	metricVillagesSkipped.WithLabelValues("unknown").Set(float64(survey.Villages.Unknown))
	metricVillagesSkipped.WithLabelValues("limit").Set(float64(survey.Villages.OverLimit))
}
