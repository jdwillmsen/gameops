package presence

import "context"

// Gate decides when the connect loop may hold a session.
type Gate interface {
	// Admit blocks until the bot should be in the world, then returns a
	// context the gate cancels when it no longer should. It fails only when
	// ctx ends.
	Admit(ctx context.Context) (context.Context, context.CancelFunc, error)
	// Connected records whether a session is spawned, for status reports.
	Connected(bool)
}

// AlwaysPresent is the gate with the feature off: it admits at once and
// never ends a session, which is the bot as it was before presence existed.
type AlwaysPresent struct{}

func (AlwaysPresent) Admit(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	sess, cancel := context.WithCancel(ctx)
	return sess, cancel, nil
}

func (AlwaysPresent) Connected(bool) {}
