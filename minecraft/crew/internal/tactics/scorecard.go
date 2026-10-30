package tactics

// Scenario kinds a fight can be scored under.
const (
	ScenarioMob  = "mob"  // warriors against a scripted mob set
	ScenarioDuel = "duel" // warriors against warriors (bot vs bot)
	ScenarioBoss = "boss" // a boss fight
	ScenarioWave = "wave" // a gauntlet wave
)

// Outcome values of a scored fight.
const (
	OutcomeWin     = "win"
	OutcomeLoss    = "loss"
	OutcomeTimeout = "timeout"
	OutcomeAborted = "aborted" // measured nothing; the trainer ignores it
)

// Scorecard is one line of the training log: what was fought, with which
// profile, and the numbers the trainer scores. Fights with Outcome aborted
// carry no signal.
type Scorecard struct {
	TS          string         `json:"ts"`
	RunID       string         `json:"runId"`
	Scenario    string         `json:"scenario"`
	Family      Family         `json:"family"`
	Opponent    string         `json:"opponent"`
	Profile     string         `json:"profile"`
	Side        string         `json:"side,omitempty"`
	Outcome     string         `json:"outcome"`
	Seconds     float64        `json:"seconds"`
	Kills       int            `json:"kills"`
	Deaths      map[string]int `json:"deaths"`
	DamageDealt float64        `json:"damageDealt"`
	DamageTaken float64        `json:"damageTaken"`
	Seed        int64          `json:"seed,omitempty"`
}

// TotalDeaths sums the per-unit death counts.
func (s Scorecard) TotalDeaths() int {
	n := 0
	for _, d := range s.Deaths {
		n += d
	}
	return n
}

// Score weights, sized for a 20 HP unit (ten hearts). Points are per health
// point.
const (
	// A whole health bar lost is 6 points: well short of the 15 a death costs,
	// which already includes the bar, yet enough that two wins in the same time
	// are told apart by who bled less. At the old 0.1 a bar was worth 2 points,
	// less than a second of speed bonus, so damage barely steered the trainer.
	damageTakenWeight = 0.3
	// Damage dealt is progress, so it is credited only to a fight that was not
	// won: a won fight dealt the mobs' whole health, which says nothing about
	// the profile, and a squad's sum double-counts a teammate's blows. A lost
	// or timed-out fight is told apart by how far it got, at 0.2 a point (a
	// 20 HP mob fully taken apart is 4), and the credit is capped below the 30
	// that separate a loss from a timeout so progress never turns a loss into
	// a better outcome than a timeout.
	damageDealtWeight = 0.2
	maxDealtCredit    = 15.0
)

// Score is the scalar the trainer maximises: a win is worth most, speed adds,
// deaths and damage taken subtract, and a fight that was not won gets a little
// credit for the damage it dealt. Weights are deliberately plain so a human can
// read why a profile was kept.
func (s Scorecard) Score() float64 {
	if s.Outcome == OutcomeAborted {
		return 0
	}
	v := 0.0
	switch s.Outcome {
	case OutcomeWin:
		v += 100
		if s.Seconds > 0 {
			v += 600 / (s.Seconds + 30)
		}
	case OutcomeTimeout:
		v -= 20
		v += s.dealtCredit()
	case OutcomeLoss:
		v -= 50
		v += s.dealtCredit()
	}
	v += float64(s.Kills) * 2
	v -= float64(s.TotalDeaths()) * 15
	v -= s.DamageTaken * damageTakenWeight
	return v
}

func (s Scorecard) dealtCredit() float64 {
	return min(max(s.DamageDealt, 0)*damageDealtWeight, maxDealtCredit)
}
