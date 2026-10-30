package arena

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	// A lab squad's id carries this mark and nothing else may be a duel's
	// squad: the squad service refuses a duel for any other, and the policy
	// refuses first so a mistyped production squad never gets a request.
	labSquadPrefix = "lab-"
	squadPutWait   = 10 * time.Second
	// Enough of an error body to say why, never enough to echo a secret.
	maxSquadErrBody = 256
	// The round id rides a squad's wire in a 32-character field.
	maxRoundID = 32
	// A beat is small and the next poll brings another, so it never waits
	// as long as a state change does.
	squadBeatWait = 5 * time.Second
)

// labSquadID is the whole shape of a lab squad id. The prefix alone would let
// "lab-/../fwb" walk out of the squad route.
var labSquadID = regexp.MustCompile(`^` + labSquadPrefix + `[a-z0-9-]+$`)

// ValidateLabSquad refuses an id that is not a lab squad.
func ValidateLabSquad(id string) error {
	if !labSquadID.MatchString(id) {
		return fmt.Errorf("arena: %q is not a lab squad (want %s followed by a-z, 0-9, -)", id, labSquadPrefix)
	}
	return nil
}

// squadDuel mirrors the squad service's duel body.
type squadDuel struct {
	State      string   `json:"state"`
	Round      string   `json:"round,omitempty"`
	Fighters   []string `json:"fighters,omitempty"`
	Enemies    []string `json:"enemies,omitempty"`
	Eliminated []string `json:"eliminated,omitempty"`
}

// SquadPolicy is the engine half of a duel: it PUTs the duel's state to each
// side's lab squad, whose units read it and hunt the other side only while it
// says go. Tags, when set, still marks the duel in the world; that is
// bookkeeping for selectors and observers, the units do not read it.
//
// A unit that cannot hear its squad stops hunting, so every failure here is
// safe; Disarm runs on a fresh context so a cancelled duel still withdraws
// the signal.
//
// A squad is a team, not a side: with --rounds the teams swap sides, and a
// squad's units must keep hunting the other squad's. Each squad is told its
// own members as the fighters and the other squad's as the enemies, whichever
// side they stand on this round.
type SquadPolicy struct {
	Base      *url.URL
	RedSquad  string
	BlueSquad string
	// Token is the shared unit token. It reaches the wire only as a bearer
	// header and no error carries it.
	Token  string
	Client *http.Client
	Tags   TargetPolicy
	// RunID prefixes the round ids the squads see.
	RunID string

	// squadOf says which squad a duelist belongs to.
	squadOf map[string]string

	armed   Roster
	members map[string][]string
	rounds  int
	round   string
	gone    bool
	out     []string
}

// NewSquadPolicy checks the target before anything is sent: an http(s) base,
// two distinct lab squads and a token. redUnits are the duelists of redSquad
// and blueUnits those of blueSquad, whichever side each stands on.
func NewSquadPolicy(base *url.URL, redSquad, blueSquad, token string, redUnits, blueUnits []string, tags TargetPolicy) (*SquadPolicy, error) {
	if base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("arena: squad URL must be an http or https URL with a host")
	}
	if token == "" {
		return nil, errors.New("arena: a squad duel needs the control token")
	}
	for _, id := range []string{redSquad, blueSquad} {
		if err := ValidateLabSquad(id); err != nil {
			return nil, err
		}
	}
	if redSquad == blueSquad {
		return nil, errors.New("arena: the two sides need two squads; a squad has one duel state")
	}
	squadOf := map[string]string{}
	for squad, units := range map[string][]string{redSquad: redUnits, blueSquad: blueUnits} {
		for _, u := range units {
			squadOf[u] = squad
		}
	}
	if len(squadOf) != len(redUnits)+len(blueUnits) || len(redUnits) == 0 || len(blueUnits) == 0 {
		return nil, errors.New("arena: each squad needs duelists of its own, and no duelist may be in both")
	}
	return &SquadPolicy{Base: base, RedSquad: redSquad, BlueSquad: blueSquad, Token: token, squadOf: squadOf, Tags: tags}, nil
}

// split groups a round's roster by squad and checks it is a duel the squads
// can be told: every duelist known, and each squad wholly on one side with
// the other squad on the opposite one.
func (p *SquadPolicy) split(r Roster) (map[string][]string, error) {
	members := map[string][]string{}
	sideOfSquad := map[string]string{}
	for _, side := range []struct {
		name  string
		units []string
	}{{SideRed, r.Red}, {SideBlue, r.Blue}} {
		for _, u := range side.units {
			squad, ok := p.squadOf[u]
			if !ok {
				return nil, fmt.Errorf("arena: duelist %q is in neither squad", u)
			}
			if prev, seen := sideOfSquad[squad]; seen && prev != side.name {
				return nil, fmt.Errorf("arena: squad %q is on both sides of the round", squad)
			}
			sideOfSquad[squad] = side.name
			members[squad] = append(members[squad], u)
		}
	}
	if len(members[p.RedSquad]) == 0 || len(members[p.BlueSquad]) == 0 || sideOfSquad[p.RedSquad] == sideOfSquad[p.BlueSquad] {
		return nil, errors.New("arena: the two squads must stand on opposite sides")
	}
	return members, nil
}

func (p *SquadPolicy) nextRound() string {
	p.rounds++
	id := p.RunID
	if id == "" {
		id = "duel"
	}
	suffix := fmt.Sprintf("-%d", p.rounds)
	if len(id)+len(suffix) > maxRoundID {
		id = id[:maxRoundID-len(suffix)]
	}
	return id + suffix
}

// bodies is the duel as each squad is to hear it: its own units as the
// fighters, the other squad's as the enemies.
func (p *SquadPolicy) bodies(state string) (red, blue squadDuel) {
	one := func(own, other string) squadDuel {
		return squadDuel{State: state, Round: p.round, Fighters: slices.Clone(p.members[own]),
			Enemies: slices.Clone(p.members[other]), Eliminated: slices.Clone(p.out)}
	}
	return one(p.RedSquad, p.BlueSquad), one(p.BlueSquad, p.RedSquad)
}

func (p *SquadPolicy) putBoth(ctx context.Context, state string) error {
	red, blue := p.bodies(state)
	return errors.Join(p.put(ctx, p.RedSquad, red), p.put(ctx, p.BlueSquad, blue))
}

// Arm tells both squads who is in the duel and that nobody fires yet.
func (p *SquadPolicy) Arm(r Roster) error {
	for _, n := range append(slices.Clone(r.Red), r.Blue...) {
		if err := validateWarriorName(n); err != nil {
			return err
		}
	}
	members, err := p.split(r)
	if err != nil {
		return err
	}
	p.armed, p.members = Roster{Red: slices.Clone(r.Red), Blue: slices.Clone(r.Blue)}, members
	p.round, p.out, p.gone = p.nextRound(), nil, false
	if p.Tags != nil {
		if err := p.Tags.Arm(r); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*squadPutWait)
	defer cancel()
	return p.putBoth(ctx, "armed")
}

// Go sends the start signal. The world tags go first: a tag that cannot be
// set stops the duel before any unit has been told to fight.
func (p *SquadPolicy) Go() error {
	if p.Tags != nil {
		if err := p.Tags.Go(); err != nil {
			return err
		}
	}
	p.gone = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*squadPutWait)
	defer cancel()
	return p.putBoth(ctx, "go")
}

// Beat renews the start signal on both squads, which let it lapse when
// nobody does. It sends nothing before the start signal: an armed duel
// fires no one, so there is nothing a lapse could leave running.
func (p *SquadPolicy) Beat() error {
	if !p.gone {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), squadBeatWait)
	defer cancel()
	return p.putBoth(ctx, "go")
}

// Eliminated takes the named duelist out of both squads' targets. A duelist
// outside the roster is no one's target, so it is only tagged.
func (p *SquadPolicy) Eliminated(name string) error {
	if _, inRoster := p.armed.SideOf(name); inRoster && !slices.Contains(p.out, name) {
		p.out = append(p.out, name)
		ctx, cancel := context.WithTimeout(context.Background(), 2*squadPutWait)
		defer cancel()
		if err := p.putBoth(ctx, "go"); err != nil {
			return err
		}
	}
	if p.Tags != nil {
		return p.Tags.Eliminated(name)
	}
	return nil
}

// Disarm withdraws the duel from both squads — over once the start signal
// went, idle before — and clears the tags. Both squads are tried whatever
// the first answers, and it takes a context of its own, since it runs after
// an interrupt that cancelled the duel's.
func (p *SquadPolicy) Disarm() error {
	state := "idle"
	if p.gone {
		state = "over"
	}
	p.gone = false
	ctx, cancel := context.WithTimeout(context.Background(), 2*squadPutWait)
	defer cancel()
	body := squadDuel{State: state, Round: p.round}
	err := errors.Join(p.put(ctx, p.RedSquad, body), p.put(ctx, p.BlueSquad, body))
	p.armed, p.members, p.out = Roster{}, nil, nil
	if p.Tags != nil {
		err = errors.Join(err, p.Tags.Disarm())
	}
	return err
}

func (p *SquadPolicy) put(ctx context.Context, squad string, d squadDuel) error {
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, squadPutWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		p.Base.JoinPath("v1", "squads", squad, "duel").String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Content-Type", "application/json")
	c := p.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		// A transport error names the URL, not the headers.
		return fmt.Errorf("arena: put duel to squad %q: %w", squad, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxSquadErrBody))
	// A server that echoes the request back must not put the token in an error.
	text := strings.TrimSpace(string(msg))
	if p.Token != "" {
		text = strings.ReplaceAll(text, p.Token, "[redacted]")
	}
	return fmt.Errorf("arena: put duel to squad %q: status %d: %s", squad, resp.StatusCode, text)
}
