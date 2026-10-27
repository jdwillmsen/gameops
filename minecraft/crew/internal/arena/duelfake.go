package arena

import (
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// FakePolicy records the calls a duel makes on its TargetPolicy, so tests and
// the engine stream can check the call order without an engine.
type FakePolicy struct {
	Calls  []string
	Roster Roster
	Out    []string
	// OnGo runs when the start signal is sent.
	OnGo func()
	// OnDisarm runs when the duel ends a round.
	OnDisarm func()
	// ArmErr is returned from Arm.
	ArmErr error
}

func (f *FakePolicy) Arm(r Roster) error {
	f.Calls = append(f.Calls, "arm")
	f.Roster = r
	return f.ArmErr
}

func (f *FakePolicy) Go() error {
	f.Calls = append(f.Calls, "go")
	if f.OnGo != nil {
		f.OnGo()
	}
	return nil
}

func (f *FakePolicy) Eliminated(name string) error {
	f.Calls = append(f.Calls, "out:"+name)
	f.Out = append(f.Out, name)
	return nil
}

func (f *FakePolicy) Disarm() error {
	f.Calls = append(f.Calls, "disarm")
	if f.OnDisarm != nil {
		f.OnDisarm()
	}
	return nil
}

// FakeSink collects scorecards.
type FakeSink struct {
	Cards []tactics.Scorecard
	Err   error
}

func (f *FakeSink) Record(c tactics.Scorecard) error {
	f.Cards = append(f.Cards, c)
	return f.Err
}
