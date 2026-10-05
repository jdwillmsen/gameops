package plugins

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/plugin"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/worlddamage"
)

// DamageWatch is what the world-damage plugin needs of the watch: the damage
// on record, and accepting it.
type DamageWatch interface {
	Current(ctx context.Context) (mapclient.World, bool)
	Clear(ctx context.Context) (mapclient.World, error)
}

var _ DamageWatch = (*worlddamage.Watch)(nil)

// maxListedChunks bounds the coordinates in one message. The map already
// caps its sample, but a whisper has to stay short enough to read, and the
// count says how large the loss is; the coordinates only say where to look.
const maxListedChunks = 8

// damageTimeLayout is how a count's time is quoted. UTC, since operators
// read it beside server logs.
const damageTimeLayout = "2006-01-02 15:04 UTC"

// WorldDamage tells joining players the world has lost chunks, and lets an
// operator accept the loss once it has been dealt with.
//
// Without the notice a player joins a damaged world unwarned and builds on
// ground a restore may take back.
type WorldDamage struct {
	watch DamageWatch
	// permissionOf resolves who is joining. A lookup that cannot place a
	// player resolves to visitor, so the operator detail fails closed.
	permissionOf func(ctx context.Context, xuid string) plugin.Permission
}

var _ plugin.Plugin = (*WorldDamage)(nil)
var _ JoinNotice = (*WorldDamage)(nil)

func NewWorldDamage(watch DamageWatch, permissionOf func(ctx context.Context, xuid string) plugin.Permission) *WorldDamage {
	return &WorldDamage{watch: watch, permissionOf: permissionOf}
}

func (*WorldDamage) Name() string { return "worlddamage" }

func (w *WorldDamage) Commands() []plugin.Command {
	return []plugin.Command{{
		Name:        "worlddamage",
		Description: "Show the lost world chunks, or accept them and stop the join notice: !worlddamage, !worlddamage clear.",
		// Operator: it names where the loss is, and clearing forgets it.
		Permission: plugin.PermissionOperator,
		// The reply names where the world is damaged, which a visitor's
		// join notice deliberately withholds.
		RedactReply: true,
		Run:         w.run,
	}}
}

const damageUsage = "!worlddamage shows the recorded loss; !worlddamage clear accepts it and stops the join notice."

func (w *WorldDamage) run(ctx context.Context, _ *plugin.Context, inv plugin.Invocation) (string, error) {
	switch {
	case len(inv.Args) == 0:
		held, ok := w.watch.Current(ctx)
		if !ok {
			return noDamage, nil
		}
		return strings.Join(operatorNotice(held), " "), nil
	case len(inv.Args) == 1 && strings.EqualFold(inv.Args[0], "clear"):
		return w.clear(ctx)
	}
	return damageUsage, nil
}

const noDamage = "No world damage is recorded."

func (w *WorldDamage) clear(ctx context.Context) (string, error) {
	cleared, err := w.watch.Clear(ctx)
	switch {
	case errors.Is(err, worlddamage.ErrNothingToClear):
		return noDamage, nil
	case errors.Is(err, mapclient.ErrCountReplaced):
		// Said as a reply and not returned: the generic failure tells an
		// operator nothing, and this one has a next step.
		newer, _ := w.watch.Current(ctx)
		return fmt.Sprintf("Not cleared: a newer chunk count has replaced the one I was quoting, and it reads %d lost chunks. Say !worlddamage to read it, then !worlddamage clear again.", newer.LostTotal), nil
	case err != nil:
		return "", fmt.Errorf("worlddamage: clear: %w", err)
	}
	return fmt.Sprintf("World damage cleared: the map accepted the count of %d lost chunks from %s. Joining players are no longer warned.", cleared.LostTotal, cleared.CheckedAt.UTC().Format(damageTimeLayout)), nil
}

// NoticeFor satisfies JoinNotice. Operators get the count and where the loss
// is; everyone else gets the plain warning, since coordinates of lost
// builds are not theirs to be handed and the warning is what they can act on.
func (w *WorldDamage) NoticeFor(ctx context.Context, xuid string) []string {
	held, ok := w.watch.Current(ctx)
	if !ok {
		return nil
	}
	if w.permissionOf(ctx, xuid) >= plugin.PermissionOperator {
		return operatorNotice(held)
	}
	return []string{"Heads up: this world is damaged, and part of its data was lost for good. If it is restored, what you build or do may be rolled back. Ask an operator before building anything you would hate to lose."}
}

func operatorNotice(held mapclient.World) []string {
	lines := []string{fmt.Sprintf("World damage: %d chunks lost (%s), counted %s. Joining players are being warned that a restore may roll back what they do.",
		held.LostTotal, lostByDimension(held.Lost), held.CheckedAt.UTC().Format(damageTimeLayout))}

	where := held.LostSample[:min(len(held.LostSample), maxListedChunks)]
	if len(where) > 0 {
		parts := make([]string, len(where))
		for i, c := range where {
			parts[i] = fmt.Sprintf("%s %d %d", c.Dimension, c.X, c.Z)
		}
		lines = append(lines, fmt.Sprintf("Lowest lost chunks (block x z): %s.", strings.Join(parts, ", ")))
	}
	return append(lines, "Say !worlddamage clear once the world is restored or the loss is accepted.")
}

// lostByDimension lists dimensions in the order players know them, then
// anything else the map reports, so the line is the same every time.
func lostByDimension(lost map[string]int) string {
	order := []string{"overworld", "nether", "end"}
	for _, name := range slices.Sorted(maps.Keys(lost)) {
		if !slices.Contains(order, name) {
			order = append(order, name)
		}
	}
	var parts []string
	for _, name := range order {
		if n := lost[name]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", name, n))
		}
	}
	return strings.Join(parts, ", ")
}
