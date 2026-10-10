package structures

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"maps"
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
		Help: "Sites worked out from the seed and offered to the page, by dimension, kind and certainty: predicted, or candidate where the terrain is not generated and the biome will decide. Zero while the kind is not verified.",
	}, []string{"dimension", "kind", "certainty"})
	metricSeedVerified = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_seed_verified",
		Help: "1 while the structures the world recorded are where the seed says they would be, without which no kind is predicted.",
	})
	metricKindVerified = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_structures_kind_verified",
		Help: "1 while the seed is verified and the world's structures of this kind are where the kind's own rule puts them, which is what lets the kind be predicted, by dimension and kind.",
	}, []string{"dimension", "kind"})
	metricDisagreements = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_structures_prediction_disagreements",
		Help: "Places where the seed and the world's records disagree, by kind: a recorded structure no site explains, or a site in a finished chunk that suits the kind with nothing recorded.",
	}, []string{"dimension", "kind"})
	metricSeeds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_generation_seeds",
		Help: "How many seeds the world's chunks say they were generated from, at the last survey: 0 for a world that does not say, in which every chunk is taken to be of the one seed in hand.",
	})
	metricSeedless = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_chunks_without_seed",
		Help: "Chunks that do not say which seed generated them, in a world whose other chunks do, at the last survey. No site is worked out for one.",
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
	metricContents = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_structures_contents",
		Help: "Saved mobs and block entities the last survey kept to set inside structures, by sort: mob or block.",
	}, []string{"sort"})
	metricContentsSkipped = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_structures_contents_skipped",
		Help: "What the last survey left out of what structures hold, by reason: malformed (an actor, block entity or village record that did not parse), limit (mobs and block entities past the bound).",
	}, []string{"reason"})
	metricDetailSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_structures_detail_duration_seconds",
		Help: "How long the last survey took to set what the world holds inside its structures, after the pass that read it.",
	})
	metricDetailFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_structures_detail_failures_total",
		Help: "Times a survey ran out of time finding the structures known by their blocks, or setting what the world holds inside its structures, and was served without that.",
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
	maxSites = 20_000
	// MaxPerKind is the most predictions of one kind kept for a dimension,
	// so that a kind with a site every 32 chunks cannot use up the layer
	// before one with a site every 80 is reached. Past it the sites
	// nearest the middle of the generated world are the ones kept.
	MaxPerKind = 500
	// foundedShare is the least of a founded kind's recorded structures,
	// as one in this many, that have to be on a site. A wrong rule puts a
	// site by one village in eleven; the FWB world's villages are on one
	// four times in nine, the rest being players' own.
	foundedShare = 5
	maxFindings  = 50
	// villageTimeout is how long the village records may take to read.
	// They are under one prefix and take milliseconds; this is what keeps
	// a world that has made them enormous from holding up the cycle.
	villageTimeout = 10 * time.Second
	// detailTimeout is how long setting what the world holds inside its
	// structures may take. It is a few milliseconds for a real world.
	detailTimeout = 10 * time.Second
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

// Prediction is a site the seed gives a kind of structure.
type Prediction struct {
	Kind Kind  `json:"kind"`
	X    int32 `json:"x"`
	Z    int32 `json:"z"`
	// Generated is set where the site's chunk is already complete, in a
	// biome the kind is built in, and the world recorded nothing there:
	// the prediction and the world disagree.
	Generated bool `json:"generated,omitempty"`
	// Candidate is set for a kind the biome decides, where the site's
	// chunk is not generated yet: the generator will try here, and
	// nothing can say what it will find.
	Candidate bool `json:"candidate,omitempty"`
	// Mapped is set where one of the world's own explorer maps points at
	// the site, in country not generated yet: the game has worked out
	// that one will be built here, which is more than a possible site.
	Mapped bool `json:"mapped,omitempty"`
}

// BiomeAt is the biome at the top of a block column as the world last
// stored it, and false where it has stored none.
type BiomeAt func(d chunks.Dimension, x, z int32) (id uint32, known bool)

// Layer is one dimension's structures.
type Layer struct {
	Recorded     []Structure
	RecordedMore int
	// Details is what the save holds inside each recorded structure, in
	// the same order, or nil for a survey that could not work it out.
	Details       []*Detail
	Predicted     []Prediction
	PredictedMore int
}

// KindCheck is how one kind's rule fared against the world's records of
// that kind.
type KindCheck struct {
	// State is the seed's while that is not verified, since no rule can
	// be judged by a seed that is not known to be right. Under a verified
	// seed it is the rule's own: verified, unverified with too few
	// recorded to tell, or refuted.
	State string `json:"state"`
	// Agree and Disagree count the kind's recorded structures a site
	// explains, and those none does.
	Agree    int `json:"agree"`
	Disagree int `json:"disagree"`
	// Built and Empty count the sites in finished chunks whose biome
	// suits the kind: those with a recorded structure, and those without.
	// A site whose biome is not known is in neither.
	Built int `json:"-"`
	Empty int `json:"-"`
	// Findings is how many of the check's disagreements are this kind's.
	Findings int `json:"-"`
	// Set aside is how many of the world's seeds the kind's rule is not
	// used for, because the structures in the chunks of that seed are not
	// where it puts them: a game version that placed the kind another way.
	SetAside int `json:"-"`

	// What the state is settled from: the structures in the chunks of
	// every seed that has not been set aside.
	borneAgree, borneDisagree int
}

// Rule names one kind's rule in one dimension. A ruined portal has a rule
// in the overworld and another in the Nether.
type Rule struct {
	Kind      Kind
	Dimension chunks.Dimension
}

// Check is the result of comparing the seed's sites with the world.
type Check struct {
	// State is how far the seed itself has been checked.
	State string
	// Kinds is each predicted kind's own result, in each dimension it has
	// a rule for.
	Kinds map[Rule]KindCheck
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
	// StructureSeed is the 32 bits structure placement is worked out from
	// in chunks not generated yet, when there are any; Check says whether
	// the world bears them out. It is not served either.
	StructureSeed    uint32
	HasStructureSeed bool
	// Seeds is how many seeds the world's chunks say they were generated
	// from, or nought for a world that does not say; Seedless is the
	// chunks of such a world that name none.
	Seeds, Seedless int
	// Areas is how many recorded areas were read; the rest were left out.
	Areas, Malformed, Unknown, OverLimit int
	// Villages is what the village records came to.
	Villages VillageStats
	// Contents is what was read of what structures hold. Detailed is false
	// for a survey that ran out of time setting it inside them.
	Contents ContentStats
	Detailed bool

	// Every village found, before any layer's limit, for the next survey
	// to fall back on, and what else each one's records held.
	villages       map[chunks.Dimension][]Structure
	villageRecords map[*VillageFacts]*villageRecords
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
	// SeedFile is where a seed worked out from the world's own records is
	// kept. Set, and a seed the world refutes is searched for instead of
	// being left as it is; an operator's seed is never second-guessed.
	SeedFile string
	// Background is what cancels a search, which outlives the survey that
	// started it; nil and nothing does.
	Background context.Context
	// Biomes says what biome a generated block is in. Without it a site
	// the biome decides is only ever a candidate, and is not shown at all
	// in terrain that is generated.
	Biomes BiomeAt
	// VillageTimeout bounds the read of the village records within a
	// survey; zero means ten seconds. DetailTimeout bounds setting what
	// the world holds inside its structures, the same way.
	VillageTimeout time.Duration
	DetailTimeout  time.Duration
	Logger         *slog.Logger

	// Limits, for tests; zero means maxAreas, MaxPerLayer and maxVillages.
	areaLimit, layerLimit, villageLimit int

	mu       sync.Mutex
	last     Survey
	surveyed bool
	// What was last logged, so that a finding is reported when it appears
	// and not again every cycle while it stands, and a kind's standing
	// when it changes.
	logged   []string
	standing map[Rule]string
	// The search for a seed: the one it found, the records last searched
	// so that they are not searched twice, and whether SeedFile was read.
	found     *uint32
	searched  uint64
	searching bool
	restored  bool
	// search stands in for Solve in tests.
	search func(context.Context, []Evidence, int) (uint32, int, error)
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
	var moved []Rule
	for _, p := range s.Predictors {
		rule := Rule{p.Kind(), p.Dimension()}
		if k, checked := survey.Check.Kinds[rule]; checked && s.standing[rule] != k.State {
			moved = append(moved, rule)
		}
	}
	s.standing = map[Rule]string{}
	for rule, k := range survey.Check.Kinds {
		s.standing[rule] = k.State
	}
	s.mu.Unlock()

	export(survey)
	if v := survey.Villages; !v.Stale {
		metricVillagesAt.Set(float64(at.Unix()))
		s.Logger.Info("villages read", "found", v.Found, "empty", v.Empty, "malformed", v.Malformed, "unknown", v.Unknown, "over_limit", v.OverLimit)
	}
	s.Logger.Info("structure contents read", "mobs", survey.Contents.Mobs, "block_entities", survey.Contents.Blocks,
		"skipped", survey.Contents.Skipped, "over_limit", survey.Contents.MobsOver+survey.Contents.BlocksOver, "detailed", survey.Detailed)
	metricSurveyAt.Set(float64(at.Unix()))
	metricSurveySeconds.Set(time.Since(started).Seconds())
	for _, rule := range moved {
		k := survey.Check.Kinds[rule]
		log := s.Logger.Info
		// The seed is right and this kind's own structures are not where
		// its rule puts them: the rule is wrong for this game version.
		if k.State == SeedRefuted && survey.Check.State == SeedVerified {
			log = s.Logger.Warn
		}
		log("structure kind checked against the world's own; it is predicted only while verified",
			"kind", string(rule.Kind), "state", k.State, "dimension", rule.Dimension.Name(), "on_a_site", k.Agree, "on_none", k.Disagree, "seeds_set_aside", k.SetAside)
	}
	switch {
	case !changed:
	case survey.Check.State == SeedRefuted:
		// Every recorded structure is a finding then, and one line says
		// what all of them mean.
		s.Logger.Warn("the seed does not put this world's recorded structures where they are, so nothing is predicted; if the world generates from another seed it is worked out from those structures unless that is switched off, and STRUCTURE_SEED supplies it otherwise",
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

	// Which seed each chunk says it was generated from, where the world
	// says. One that cannot be read is a world that does not say.
	var book *seedBook
	if raw, err := db.Get([]byte(dictionaryKey), nil); err == nil {
		if book, err = readSeedBook(raw); err != nil {
			survey.Malformed++
			s.Logger.Warn("the world's record of which seed generated each chunk was not read; every chunk is taken to be of the one seed in hand", "error", err)
		}
	}

	pieces := map[chunks.Dimension][]piece{}
	extents := map[chunks.Dimension]*extent{}
	held := newContents()
	it := db.NewIterator(nil, nil)
	defer it.Release()
	for n := 0; it.Next(); n++ {
		if n%65536 == 0 && ctx.Err() != nil {
			return Survey{}, ctx.Err()
		}
		key := it.Key()
		switch {
		case bytes.HasPrefix(key, actorPrefix):
			held.actor(key, it.Value())
			continue
		case bytes.HasPrefix(key, digpPrefix):
			held.place(key, it.Value())
			continue
		case bytes.HasPrefix(key, mapPrefix):
			held.mapRecord(it.Value())
			continue
		}
		pos, tag, ok := chunks.RecordOf(key)
		if !ok {
			continue
		}
		switch tag {
		case TagBlockEntities:
			// The tag alone, or it would be a sub-chunk's key.
			if len(key) == 9 || len(key) == 13 {
				held.blockEntities(pos, it.Value())
			}
		case TagFinalized:
			e, seen := extents[pos.Dim]
			if !seen {
				e = newExtent(pos)
				extents[pos.Dim] = e
			}
			e.add(pos)
			if v := it.Value(); len(v) >= 4 && binary.LittleEndian.Uint32(v) == finalizedDone {
				e.finish(pos)
			}
		case TagGeneration:
			// It follows the chunk's record 54 in the order of the keys, so
			// the chunk is already one of the extent's.
			if e, v := extents[pos.Dim], it.Value(); e != nil && book != nil && len(v) == 8 {
				if at, named := book.byHash[binary.LittleEndian.Uint64(v)]; named {
					e.born(pos, at)
				}
			}
		case TagVolumes:
			found, unknown, malformed, err := decodeVolumes(pos, it.Value())
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
	// The level is read before the villages: it is what says whether the
	// villages of the survey before are this world's to fall back on.
	seeds, current, searched := s.seeds(worldDir, &survey, book)
	for _, e := range extents {
		// A world that does not say which seed made a chunk is all of the
		// one seed.
		e.single = survey.Seeds == 0
		if !e.single {
			survey.Seedless += e.seedless()
		}
	}
	if current >= 0 {
		survey.StructureSeed, survey.HasStructureSeed = uint32(seeds[current].whole), true
	}
	if survey.villages, survey.villageRecords, survey.Villages, err = s.readVillages(ctx, db, survey); err != nil {
		return Survey{}, err
	}
	found, err := s.locate(ctx, held, recorded)
	if err != nil {
		return Survey{}, err
	}
	survey.Contents = held.stats
	survey.Contents.Targets = len(held.targets)
	for _, r := range survey.villageRecords {
		survey.Contents.Skipped += r.skipped
	}

	predicted := map[chunks.Dimension][]Prediction{}
	more := map[chunks.Dimension]int{}
	if len(seeds) > 0 && len(s.Predictors) > 0 {
		// A village is recorded apart from the rest and is checked like
		// them: every one read, whatever the layer goes on to keep.
		known := map[chunks.Dimension][]Structure{}
		for _, d := range chunks.Dimensions {
			known[d] = append(append(slices.Clip(recorded[d]), survey.villages[d]...), found[d]...)
		}
		survey.Check, predicted, more = compare(s.Predictors, seeds, current, layerLimit, known, held.targets, extents, s.Biomes)
		// Only a seed that was this service's own to choose is searched
		// past: the world's word for its chunks is not.
		if searched {
			s.reconsider(survey.Check, known)
		}
	}

	for _, d := range chunks.Dimensions {
		// Villages come after the structures the seed is checked against,
		// and the ones found by their blocks after those, so that what
		// the world records least of is the first to go where a layer is
		// cut short.
		layer := Layer{Recorded: append(append(slices.Clip(recorded[d]), survey.villages[d]...), found[d]...)}
		if len(layer.Recorded) > layerLimit {
			layer.RecordedMore = len(layer.Recorded) - layerLimit
			layer.Recorded = layer.Recorded[:layerLimit]
		}
		// compare leaves out every kind the world does not bear out, and
		// none is borne out under a seed that is not.
		if survey.Check.State == SeedVerified {
			layer.Predicted, layer.PredictedMore = predicted[d], more[d]
		}
		survey.Layers[d] = layer
	}
	s.detail(ctx, &survey, held)
	return survey, nil
}

// locate finds the kinds that are found by their blocks, in each dimension,
// within the time the details have. Running out of it is not the survey's
// failure: the kinds the world records are served without them. One the
// world has also recorded, as a newer game records an igloo an older one
// left only a chest of, is left to its record.
func (s *Surveyor) locate(ctx context.Context, held *contents, recorded map[chunks.Dimension][]Structure) (map[chunks.Dimension][]Structure, error) {
	timeout := s.DetailTimeout
	if timeout == 0 {
		timeout = detailTimeout
	}
	within, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out := map[chunks.Dimension][]Structure{}
	for _, d := range chunks.Dimensions {
		found, err := held.locate(within, d)
		switch {
		case err == nil:
			out[d] = unrecorded(found, recorded[d])
			continue
		case ctx.Err() != nil:
			// The survey itself was stopped, which is its failure to report.
			return nil, ctx.Err()
		}
		metricDetailFailures.Inc()
		s.Logger.Error("the structures found by their blocks were not worked out; the rest are served without them", "error", err)
		return nil, nil
	}
	return out, nil
}

// recordedPad is how far outside a recorded structure's box, in blocks
// across the map, what a structure of its kind was found by may lie and
// still be that structure: an igloo's basement runs out from under it.
const recordedPad = 16

// unrecorded is the found structures that no recorded one of the same kind
// is.
func unrecorded(found, recorded []Structure) []Structure {
	if len(recorded) == 0 {
		return found
	}
	byKind := map[Kind][]Box{}
	for _, r := range recorded {
		byKind[r.Kind] = append(byKind[r.Kind], r.Box)
	}
	return slices.DeleteFunc(found, func(f Structure) bool {
		return slices.ContainsFunc(byKind[f.Kind], func(b Box) bool {
			return f.MinX <= b.MaxX+recordedPad && f.MaxX >= b.MinX-recordedPad && f.MinZ <= b.MaxZ+recordedPad && f.MaxZ >= b.MinZ-recordedPad
		})
	})
}

// detail sets what the world holds inside each structure, within its own
// time. Running out of it is not the survey's failure: the structures are
// served as they are, without what is in them.
func (s *Surveyor) detail(ctx context.Context, survey *Survey, held *contents) {
	timeout := s.DetailTimeout
	if timeout == 0 {
		timeout = detailTimeout
	}
	started := time.Now()
	within, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	left := &allowance{names: maxDetailNames, places: maxDetailPlaces}
	details := map[chunks.Dimension][]*Detail{}
	for _, d := range chunks.Dimensions {
		list, err := held.describe(within, d, survey.Layers[d].Recorded, survey.villageRecords, survey.Level, left)
		if err != nil {
			metricDetailFailures.Inc()
			s.Logger.Error("what the structures hold was not worked out; they are served without it", "error", err)
			return
		}
		details[d] = list
	}
	for _, d := range chunks.Dimensions {
		layer := survey.Layers[d]
		layer.Details = details[d]
		survey.Layers[d] = layer
	}
	survey.Detailed = true
	metricDetailSeconds.Set(time.Since(started).Seconds())
}

// readVillages reads the village records within their own time. Running out
// of it, or failing to read them, is not the survey's failure: the villages
// of the last survey stand, and everything else is as fresh as it would be.
func (s *Surveyor) readVillages(ctx context.Context, db *leveldb.DB, now Survey) (map[chunks.Dimension][]Structure, map[*VillageFacts]*villageRecords, VillageStats, error) {
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
	found, records, stats, err := readVillages(within, db, limit)
	if err == nil {
		metricVillageSeconds.Set(time.Since(started).Seconds())
		return found, records, stats, nil
	}
	if ctx.Err() != nil {
		// The survey itself was stopped, which is its failure to report.
		return nil, nil, VillageStats{}, ctx.Err()
	}
	metricVillageFailures.Inc()
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	// The last survey's villages stand only for the world they were read
	// from. In another world, or an earlier copy of this one, they are
	// villages that are not there, with villagers nobody has.
	if !sameWorld(last, now) {
		s.Logger.Error("villages not read, and those of the last survey are another world's; none are served", "error", err)
		return nil, nil, VillageStats{Stale: true}, nil
	}
	stats = last.Villages
	stats.Stale = true
	s.Logger.Error("villages not read; those of the last survey are kept", "error", err, "kept", stats.Found)
	return last.villages, last.villageRecords, stats, nil
}

// sameWorld reports whether a survey is of the world the one before was
// of, and no earlier in it: the same seed, and a game tick that has not
// gone back where both hold one. A level that could not be read is no
// world to be the same as.
func sameWorld(before, now Survey) bool {
	if !before.HasLevel || !now.HasLevel || before.Level.Seed != now.Level.Seed {
		return false
	}
	return !before.Level.TickKnown || !now.Level.TickKnown || now.Level.Tick >= before.Level.Tick
}

// seeds is the seeds sites are worked out from, which of them the game
// generates new chunks from (-1 where that is not known), and whether the
// seed is one this service chose and may search past.
//
// A world that says which seed generated each chunk is taken at its word:
// every seed its chunks name, and level.dat's for the chunks still to
// come. One that does not say is all of one seed, the low half of the one
// in level.dat unless a search has found the one its records answer to.
// An operator's seed is used for everything, whatever the world says.
func (s *Surveyor) seeds(worldDir string, survey *Survey, book *seedBook) (seeds []worldSeed, current int, searched bool) {
	level, err := leveldat.Read(filepath.Join(worldDir, "level.dat"))
	if err != nil {
		// Recorded structures need no seed, so they are still served.
		s.Logger.Warn("level.dat not read; nothing is predicted in chunks not generated yet this cycle", "error", err)
	} else {
		survey.Level, survey.HasLevel = level, true
	}
	if s.StructureSeed != nil {
		return []worldSeed{{whole: int64(*s.StructureSeed), narrow: true}}, 0, false
	}
	if book != nil && len(book.seeds) > 0 {
		survey.Seeds = len(book.seeds)
		current = -1
		for i, seed := range book.seeds {
			seeds = append(seeds, worldSeed{whole: seed})
			if err == nil && seed == level.Seed {
				current = i
			}
		}
		if err == nil && current < 0 {
			// No chunk has been generated from it yet.
			seeds = append(seeds, worldSeed{whole: level.Seed})
			current = len(seeds) - 1
		}
		return seeds, current, false
	}
	if s.SeedFile != "" {
		if seed, ok := s.worked(); ok {
			return []worldSeed{{whole: int64(seed), narrow: true}}, 0, true
		}
	}
	if err != nil {
		return nil, -1, false
	}
	return []worldSeed{{whole: level.Seed}}, 0, true
}

// extent is where a dimension's chunks are: the box around all of them,
// every chunk's position, which is what the middle of the world is taken
// from, the finished ones among them, and the country they lie in. It
// comes to sixteen bytes a chunk.
type extent struct {
	Area
	xs, zs []int32
	// done is the finished chunks, each as packed by chunkKey, in order
	// once sorted is set.
	done   []uint64
	sorted bool
	// cells is every square of cellChunks chunks a side with a chunk in
	// it.
	cells map[uint64]struct{}
	// seed is which of the world's seeds generated each chunk, or
	// seedUnknown; single is set for a world that does not say, whose
	// chunks are all of the one seed.
	seed   map[uint64]int8
	single bool
}

// cellChunks is the side of the squares the generated country is kept as,
// in chunks: coarse enough that a world is a few thousand of them.
const cellChunks = 16

func chunkKey(x, z int32) uint64 { return uint64(uint32(x))<<32 | uint64(uint32(z)) }

func newExtent(p chunks.Pos) *extent {
	return &extent{Area: Area{p.X, p.Z, p.X, p.Z}, cells: map[uint64]struct{}{}, seed: map[uint64]int8{}}
}

func (e *extent) add(p chunks.Pos) {
	e.Area = Area{min(e.MinX, p.X), min(e.MinZ, p.Z), max(e.MaxX, p.X), max(e.MaxZ, p.Z)}
	e.xs = append(e.xs, p.X)
	e.zs = append(e.zs, p.Z)
	e.cells[chunkKey(floorDiv(p.X, cellChunks), floorDiv(p.Z, cellChunks))] = struct{}{}
	if _, held := e.seed[chunkKey(p.X, p.Z)]; !held {
		e.seed[chunkKey(p.X, p.Z)] = seedUnknown
	}
}

// born marks which of the world's seeds generated a chunk.
func (e *extent) born(p chunks.Pos, seed int8) {
	if _, held := e.seed[chunkKey(p.X, p.Z)]; held {
		e.seed[chunkKey(p.X, p.Z)] = seed
	}
}

// seedOf is which seed generated a chunk the world holds: seedUnknown
// where it does not say, and held false for a chunk it does not hold.
func (e *extent) seedOf(x, z int32) (seed int8, held bool) {
	seed, held = e.seed[chunkKey(x, z)]
	if held && e.single {
		return 0, true
	}
	return seed, held
}

// holds reports whether the world has the chunk at all.
func (e *extent) holds(x, z int32) bool {
	_, held := e.seed[chunkKey(x, z)]
	return held
}

func (e *extent) seedless() int {
	n := 0
	for _, seed := range e.seed {
		if seed == seedUnknown {
			n++
		}
	}
	return n
}

// finish marks a chunk the world has generated to the end.
func (e *extent) finish(p chunks.Pos) {
	e.done = append(e.done, chunkKey(p.X, p.Z))
	e.sorted = false
}

func (e *extent) finished(x, z int32) bool {
	if !e.sorted {
		slices.Sort(e.done)
		e.sorted = true
	}
	_, found := slices.BinarySearch(e.done, chunkKey(x, z))
	return found
}

// near reports whether a chunk is within predictionMargin of one the world
// holds, to the nearest cell: the country a player could walk into next.
func (e *extent) near(x, z int32) bool {
	const reach = predictionMargin / cellChunks
	cx, cz := floorDiv(x, cellChunks), floorDiv(z, cellChunks)
	for dx := int32(-reach); dx <= reach; dx++ {
		for dz := int32(-reach); dz <= reach; dz++ {
			if _, held := e.cells[chunkKey(cx+dx, cz+dz)]; held {
				return true
			}
		}
	}
	return false
}

// window is the area structures are predicted and checked in: the extent
// and a margin, kept within maxReach of the middle of the chunks. The
// middle is the median on each axis, which no number of far-off chunks
// short of half the world can move.
func (e *extent) window() (area Area, midX, midZ int32) {
	slices.Sort(e.xs)
	slices.Sort(e.zs)
	midX, midZ = e.xs[len(e.xs)/2], e.zs[len(e.zs)/2]
	return Area{
		max(e.MinX-predictionMargin, midX-maxReach), max(e.MinZ-predictionMargin, midZ-maxReach),
		min(e.MaxX+predictionMargin, midX+maxReach), min(e.MaxZ+predictionMargin, midZ+maxReach),
	}, midX, midZ
}

func (a Area) holds(b Box) bool {
	return b.MinX>>4 >= a.MinX && b.MaxX>>4 <= a.MaxX && b.MinZ>>4 >= a.MinZ && b.MaxZ>>4 <= a.MaxZ
}

// borne reports whether structures bear a rule out: more on a site than
// not, or for a founded kind at least one in foundedShare.
func borne(agree, disagree int, founded bool) bool {
	if founded {
		return agree*foundedShare >= agree+disagree
	}
	return agree > disagree
}

// settle decides a kind's state from the structures in the chunks of the
// seeds its rule has not been set aside for, under a seed in seedState.
func (c *KindCheck) settle(seedState string, founded bool) {
	held := borne(c.borneAgree, c.borneDisagree, founded)
	switch {
	case seedState != SeedVerified:
		c.State = seedState
	case c.borneAgree >= minEvidence && held:
		c.State = SeedVerified
	case c.borneAgree+c.borneDisagree >= minEvidence && !held:
		c.State = SeedRefuted
	case c.SetAside > 0 && c.borneAgree == 0:
		// Every seed with anything to judge the rule by judged against it.
		c.State = SeedRefuted
	default:
		c.State = SeedUnverified
	}
}

// sitesOf is a predictor's sites in area under one of the world's seeds,
// or false for a rule that needs more of the seed than is known. A kind
// with a site every few chunks is asked only about the regions the world
// has chunks in, since it is predicted nowhere else and a walk of the
// whole area would be cut short before it reached them.
func sitesOf(p Predictor, seed worldSeed, area Area, generated *extent) (sites []Site, more int, ok bool) {
	switch p := p.(type) {
	case whole:
		if seed.narrow {
			return nil, 0, false
		}
		sites, more = p.WholeSites(seed.whole, area, maxSites)
	case dense:
		if generated == nil {
			// One chunk is asked about: its region's site, if it is that.
			site := p.SiteIn(uint32(seed.whole), floorDiv(area.MinX, p.Region()), floorDiv(area.MinZ, p.Region()))
			if site.ChunkX >= area.MinX && site.ChunkX <= area.MaxX && site.ChunkZ >= area.MinZ && site.ChunkZ <= area.MaxZ {
				sites = append(sites, site)
			}
			break
		}
		regions := map[uint64]struct{}{}
		for key := range generated.seed {
			x, z := int32(key>>32), int32(uint32(key))
			regions[chunkKey(floorDiv(x, p.Region()), floorDiv(z, p.Region()))] = struct{}{}
		}
		for _, key := range slices.Sorted(maps.Keys(regions)) {
			site := p.SiteIn(uint32(seed.whole), int32(key>>32), int32(uint32(key)))
			if len(sites) >= maxSites {
				more++
				continue
			}
			sites = append(sites, site)
		}
	default:
		sites, more = p.Sites(uint32(seed.whole), area, maxSites)
	}
	return sites, more, true
}

// compare sets each predictor's sites beside what the world holds, and
// returns the predictions of the kinds the world bears out. A site is
// worked out from the seed of the chunk it falls in, and from the seed
// numbered current where no chunk is there yet.
func compare(
	predictors []Predictor,
	seeds []worldSeed,
	current int,
	limit int,
	recorded map[chunks.Dimension][]Structure,
	targets map[target]struct{},
	extents map[chunks.Dimension]*extent,
	biomeAt BiomeAt,
) (Check, map[chunks.Dimension][]Prediction, map[chunks.Dimension]int) {
	check := Check{Kinds: map[Rule]KindCheck{}}
	type result struct {
		p     Predictor
		found []Prediction
		more  int
	}
	var results []result
	for _, p := range predictors {
		d := p.Dimension()
		rule := Rule{p.Kind(), d}
		generated, ok := extents[d]
		if !ok {
			check.Kinds[rule] = KindCheck{}
			continue
		}
		kind := KindCheck{}
		traits := traitsOf(p)
		find := func(format string, args ...any) {
			check.Total++
			kind.Findings++
			if len(check.Findings) < maxFindings {
				check.Findings = append(check.Findings, fmt.Sprintf(format, args...))
			}
		}
		area, midX, midZ := generated.window()
		res := result{p: p}
		// The world's own of this kind, which is all a site is set beside,
		// and the seed of the chunks each is in: seedUnknown for one that
		// says nothing about any seed, being outside the window or in
		// chunks that name none.
		var own []int
		born := map[int]int8{}
		for i, real := range recorded[d] {
			if real.Kind != p.Kind() {
				continue
			}
			own = append(own, i)
			seed, held := generated.seedOf((real.MinX+(real.MaxX-real.MinX)/2)>>4, (real.MinZ+(real.MaxZ-real.MinZ)/2)>>4)
			if !held {
				seed, held = generated.seedOf(real.MinX>>4, real.MinZ>>4)
			}
			if generated.single {
				seed, held = 0, true
			}
			if !held || int(seed) >= len(seeds) {
				seed = seedUnknown
			}
			born[i] = seed
		}
		type tally struct{ agree, disagree int }
		bySeed := make([]tally, len(seeds))
		// Where the game's own maps say one of the kind is: each either
		// is a site of one of the world's seeds, which is the game agreeing
		// with the rule, or is not, which is the rule being wrong. One that
		// is a site of the seed new chunks come from is still to be built.
		mapped := map[Site]bool{}
		for t := range targets {
			if t.kind != p.Kind() || t.dim != d {
				continue
			}
			agreed := false
			for at, seed := range seeds {
				sites, _, ok := sitesOf(p, seed, Area{t.site.ChunkX, t.site.ChunkZ, t.site.ChunkX, t.site.ChunkZ}, nil)
				if ok && len(sites) == 1 && !agreed {
					agreed = true
					bySeed[at].agree++
					mapped[t.site] = mapped[t.site] || at == current
				}
			}
			if !agreed && len(seeds) > 0 {
				bySeed[max(current, 0)].disagree++
			}
		}
		// Which seed each prediction is of, beside it.
		var of []int
		explained := map[int]bool{}
		for at, seed := range seeds {
			sites, left, ok := sitesOf(p, seed, area, generated)
			if !ok {
				continue
			}
			res.more += left
			for _, site := range sites {
				// A structure is of the seed its own chunks were generated
				// from, wherever it started: one that starts in a chunk an
				// earlier seed made is still built, in part, in the chunks
				// beside it that this seed made.
				built := false
				for _, i := range own {
					if (born[i] == int8(at) || born[i] == seedUnknown) && p.Explains(site, recorded[d][i].Box) {
						explained[i], built = true, true
					}
				}
				// A chunk is what its own seed made it, and one still to
				// come will be what the current seed makes it: nothing is
				// said of a site in a chunk of another seed.
				if of, held := generated.seedOf(site.ChunkX, site.ChunkZ); (held && int(of) != at) || (!held && at != current) {
					continue
				}
				held := generated.holds(site.ChunkX, site.ChunkZ)
				done := held && generated.finished(site.ChunkX, site.ChunkZ)
				// The middle of the chunk: an outpost's own corner of it
				// has been seen in the biome next door.
				biome, known := uint32(0), false
				if biomeAt != nil && !p.Certain() {
					biome, known = biomeAt(d, site.ChunkX*16+8, site.ChunkZ*16+8)
				}
				suits := p.Certain() || (known && p.Allows(biome))
				if built {
					if done && suits {
						kind.Built++
					}
					continue
				}
				x, z := p.Centre(site)
				prediction := Prediction{Kind: p.Kind(), X: x, Z: z}
				switch {
				case p.Certain():
					prediction.Generated = done
				case known && !suits:
					continue
				case !done:
					// Only beside the world: its box takes in everything
					// between its furthest corners, most of it nowhere near
					// a chunk, and a site there is no use to anybody yet.
					if traits.FinishedOnly || !generated.near(site.ChunkX, site.ChunkZ) {
						continue
					}
					prediction.Candidate = true
				case !known:
					// Generated, nothing recorded, and no biome to say
					// whether anything was to be expected: most such sites
					// are in the wrong one.
					continue
				default:
					prediction.Generated = true
				}
				if traits.FinishedOnly && !prediction.Generated {
					continue
				}
				if prediction.Generated {
					kind.Empty++
					if traits.DropEmpty {
						continue
					}
					// The game has no record of a village nobody has been
					// near, and a chest somebody has opened no longer says
					// what it was: one missing from such a site is not a
					// finding.
					if !p.Founded() && !traits.Quiet {
						find("%s predicted at %s %d, %d: that chunk is generated and the world recorded none", p.Kind(), d.Name(), x, z)
					}
				}
				res.found = append(res.found, prediction)
				of = append(of, at)
			}
		}
		// One the game's own map points at, in country not generated, needs
		// no biome asked of it, and is offered however far off it is: the
		// game has asked, and a map is for going a long way by.
		for _, site := range slices.SortedFunc(maps.Keys(mapped), func(a, b Site) int {
			return cmp.Or(cmp.Compare(a.ChunkX, b.ChunkX), cmp.Compare(a.ChunkZ, b.ChunkZ))
		}) {
			if !mapped[site] || generated.holds(site.ChunkX, site.ChunkZ) {
				continue
			}
			x, z := p.Centre(site)
			at := slices.IndexFunc(res.found, func(p Prediction) bool { return p.X == x && p.Z == z })
			if at < 0 {
				res.found = append(res.found, Prediction{Kind: p.Kind(), X: x, Z: z})
				of = append(of, current)
				at = len(res.found) - 1
			}
			res.found[at].Candidate, res.found[at].Mapped = false, true
		}
		for _, i := range own {
			real := recorded[d][i]
			// A structure outside the window had no site looked for, so it
			// says nothing about the seed either way; and nor does one in
			// a chunk whose seed is not known.
			if !area.holds(real.Box) || born[i] == seedUnknown {
				continue
			}
			if explained[i] {
				bySeed[born[i]].agree++
				continue
			}
			bySeed[born[i]].disagree++
			if !p.Founded() && !traits.Quiet {
				find("%s recorded at %s %d, %d to %d, %d: the seed puts none there", real.Kind, d.Name(), real.MinX, real.MinZ, real.MaxX, real.MaxZ)
			}
		}
		// A game version that placed the kind another way leaves the
		// chunks of its seed disagreeing with the rule. The rule is not
		// used for those, and is judged by the rest.
		aside := make([]bool, len(seeds))
		for at, t := range bySeed {
			kind.Agree += t.agree
			kind.Disagree += t.disagree
			if t.agree+t.disagree >= minEvidence && !borne(t.agree, t.disagree, p.Founded()) {
				aside[at] = true
				kind.SetAside++
				continue
			}
			kind.borneAgree += t.agree
			kind.borneDisagree += t.disagree
		}
		if kind.SetAside > 0 {
			kept := res.found[:0]
			for i, prediction := range res.found {
				if !aside[of[i]] {
					kept = append(kept, prediction)
				}
			}
			res.found = kept
		}
		if p.Exact() {
			check.Agree += kind.Agree
			check.Disagree += kind.Disagree
		}
		if over := len(res.found) - min(limit, MaxPerKind); over > 0 {
			// What the world can already be asked about goes before what
			// it cannot.
			rank := func(p Prediction) int {
				if p.Candidate {
					return 1
				}
				return 0
			}
			away := func(p Prediction) int64 {
				dx, dz := int64(p.X>>4)-int64(midX), int64(p.Z>>4)-int64(midZ)
				return dx*dx + dz*dz
			}
			slices.SortStableFunc(res.found, func(a, b Prediction) int {
				return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(away(a), away(b)))
			})
			res.found = res.found[:len(res.found)-over]
			res.more += over
		}
		check.Kinds[rule] = kind
		results = append(results, res)
	}
	switch {
	case check.Agree >= minEvidence && check.Agree > check.Disagree:
		check.State = SeedVerified
	case check.Agree+check.Disagree >= minEvidence:
		check.State = SeedRefuted
	default:
		check.State = SeedUnverified
	}

	predicted := map[chunks.Dimension][]Prediction{}
	more := map[chunks.Dimension]int{}
	for _, res := range results {
		rule := Rule{res.p.Kind(), res.p.Dimension()}
		kind := check.Kinds[rule]
		kind.settle(check.State, res.p.Founded())
		check.Kinds[rule] = kind
		if kind.State != SeedVerified {
			continue
		}
		d := res.p.Dimension()
		room := max(limit-len(predicted[d]), 0)
		if len(res.found) > room {
			res.more += len(res.found) - room
			res.found = res.found[:room]
		}
		predicted[d] = append(predicted[d], res.found...)
		more[d] += res.more
	}
	// A kind whose dimension the world has no chunk of stands as the seed
	// lets it.
	for rule, k := range check.Kinds {
		if k.State == "" {
			k.settle(check.State, false)
			check.Kinds[rule] = k
		}
	}
	return check, predicted, more
}

func export(survey Survey) {
	for _, d := range chunks.Dimensions {
		recorded, predicted, candidates := map[Kind]int{}, map[Kind]int{}, map[Kind]int{}
		for _, r := range survey.Layers[d].Recorded {
			recorded[r.Kind]++
		}
		for _, p := range survey.Layers[d].Predicted {
			if p.Candidate {
				candidates[p.Kind]++
			} else {
				predicted[p.Kind]++
			}
		}
		for _, kind := range Kinds {
			metricRecorded.WithLabelValues(d.Name(), string(kind)).Set(float64(recorded[kind]))
			metricPredicted.WithLabelValues(d.Name(), string(kind), "predicted").Set(float64(predicted[kind]))
			metricPredicted.WithLabelValues(d.Name(), string(kind), "candidate").Set(float64(candidates[kind]))
		}
	}
	verified := 0.0
	if survey.Check.State == SeedVerified {
		verified = 1
	}
	metricSeedVerified.Set(verified)
	for rule, k := range survey.Check.Kinds {
		verified := 0.0
		if k.State == SeedVerified {
			verified = 1
		}
		metricKindVerified.WithLabelValues(rule.Dimension.Name(), string(rule.Kind)).Set(verified)
		metricDisagreements.WithLabelValues(rule.Dimension.Name(), string(rule.Kind)).Set(float64(k.Findings))
	}
	metricSeeds.Set(float64(survey.Seeds))
	metricSeedless.Set(float64(survey.Seedless))
	metricSkipped.WithLabelValues("malformed").Set(float64(survey.Malformed))
	metricSkipped.WithLabelValues("unknown").Set(float64(survey.Unknown))
	metricSkipped.WithLabelValues("limit").Set(float64(survey.OverLimit))
	metricVillagesSkipped.WithLabelValues("empty").Set(float64(survey.Villages.Empty))
	metricVillagesSkipped.WithLabelValues("malformed").Set(float64(survey.Villages.Malformed))
	metricVillagesSkipped.WithLabelValues("unknown").Set(float64(survey.Villages.Unknown))
	metricVillagesSkipped.WithLabelValues("limit").Set(float64(survey.Villages.OverLimit))
	metricContents.WithLabelValues("mob").Set(float64(survey.Contents.Mobs))
	metricContents.WithLabelValues("block").Set(float64(survey.Contents.Blocks))
	metricContentsSkipped.WithLabelValues("malformed").Set(float64(survey.Contents.Skipped))
	metricContentsSkipped.WithLabelValues("limit").Set(float64(survey.Contents.MobsOver + survey.Contents.BlocksOver))
}
