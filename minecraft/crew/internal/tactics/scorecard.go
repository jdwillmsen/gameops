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

// Score is the scalar the trainer maximises: a win is worth most, speed adds,
// deaths and damage taken subtract. Weights are deliberately plain so a human
// can read why a profile was kept.
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
	case OutcomeLoss:
		v -= 50
	}
	v += float64(s.Kills) * 2
	v -= float64(s.TotalDeaths()) * 15
	v -= s.DamageTaken * 0.1
	return v
}
