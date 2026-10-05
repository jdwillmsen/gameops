// Package adapters holds the implementations of the plugin package's
// capability interfaces (Voice, Facts, ...) that reach mc-console-bridge —
// the only thing with write access to the server console.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/chat"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/plugin"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// NoopVoice logs what it would have said instead of actually reaching the
// console bridge. Kept around as a lightweight stand-in for tests that
// exercise plugin dispatch without needing a real (or fake) bridge.
type NoopVoice struct {
	log *logging.Logger
}

// NewNoopVoice builds a NoopVoice that logs through log.
func NewNoopVoice(log *logging.Logger) NoopVoice {
	return NoopVoice{log: log}
}

var _ plugin.Voice = NoopVoice{}

func (v NoopVoice) Tell(ctx context.Context, xuid, message string) error {
	v.log.Info("voice_tell_noop", logging.Fields{"xuid": xuid, "message": message})
	return nil
}

func (v NoopVoice) Say(ctx context.Context, message string) error {
	v.log.Info("voice_say_noop", logging.Fields{"message": message})
	return nil
}

// NameResolver looks up a player's current gamertag from their XUID.
// Implemented by internal/roster.Roster: Voice.Tell only carries an XUID,
// but Bedrock's tellraw needs a selector or a name to target, so something
// has to bridge that gap. Voice never trusts a caller-supplied name for
// this — the whole point of internal/chat resolving identity by XUID only
// is defeated if a display name it never checked can steer where a reply
// goes.
type NameResolver interface {
	// NameFor returns the last gamertag recorded for xuid. ok is false only
	// when nothing has ever named this xuid — Tell must not guess or fall
	// back to xuid itself, since that is never a valid tellraw target. A
	// player who has since left, or whose session ended with the agent's
	// connection, still resolves: the tellraw then reaches nobody, which
	// costs less than a reply this process can no longer address at all.
	NameFor(xuid string) (name string, ok bool)
}

// BridgeVoice is the real console-bridge-backed Voice: every Tell/Say goes
// out as a tellraw/say console command through mc-console-bridge's
// POST /command, the only way anything in this system can speak.
type BridgeVoice struct {
	client *BridgeClient
	names  NameResolver

	mu   sync.Mutex
	said []saidLine
	now  func() time.Time
}

// saidLine is one broadcast awaiting its own echo. echo is set only when
// line carries a target selector the server will have expanded by the time
// it comes back, and then matches what the expansion can have produced.
type saidLine struct {
	line string
	echo *regexp.Regexp
	at   time.Time
}

// bareSelector matches a Bedrock target selector that occupies a whole
// token, optionally with an argument block: the forms `say` expands
// server-side. The trailing word boundary is what keeps @server -- the
// agent's own mention token, and so a routine thing for a reply to contain
// -- from being read as @s followed by "erver".
var bareSelector = regexp.MustCompile(`@(?:initiator|a|e|p|r|s)(?:\[[^\]]*\])?\b`)

// echoPattern returns a matcher for the line the server will broadcast for
// line, or nil when the two are the same string.
//
// An expanded selector is the one rewriting that defeats comparing the echo
// against what was said, so the literal text around each selector is matched
// exactly and only the selector itself is left open. Anchored, so a shorter
// or longer line cannot pass as this one.
func echoPattern(line string) *regexp.Regexp {
	spans := bareSelector.FindAllStringIndex(line, -1)
	if spans == nil {
		return nil
	}
	var pattern strings.Builder
	pattern.WriteString(`\A`)
	end := 0
	for _, span := range spans {
		pattern.WriteString(regexp.QuoteMeta(line[end:span[0]]))
		pattern.WriteString(`.*`)
		end = span[1]
	}
	pattern.WriteString(regexp.QuoteMeta(line[end:]))
	pattern.WriteString(`\z`)
	// Every variable part of the pattern went through QuoteMeta, so this
	// cannot fail on the agent's own text.
	re, err := regexp.Compile(pattern.String())
	if err != nil {
		return nil
	}
	return re
}

// sayEchoWindow bounds how long a broadcast stays recognisable as this
// agent's own. It has to outlive one console-to-chat round trip and nothing
// else; a line still unmatched after this was never echoed back.
const sayEchoWindow = 30 * time.Second

// sayEchoMemory caps the unmatched broadcasts kept, so a run of lines that
// never come back (a bridge that accepts commands the server drops) cannot
// grow this without bound between expiries.
const sayEchoMemory = 64

// NewBridgeVoice builds a BridgeVoice. names resolves the gamertag a Tell
// call's xuid should be targeted at.
func NewBridgeVoice(client *BridgeClient, names NameResolver) *BridgeVoice {
	return &BridgeVoice{client: client, names: names, now: time.Now}
}

var _ plugin.Voice = (*BridgeVoice)(nil)

// tellrawPayload is the JSON body mc-console-bridge's allowlist requires
// for a tellraw command: an object carrying a "rawtext" array. Built with
// encoding/json rather than string concatenation so a message containing
// quotes, backslashes, or other JSON-significant characters can never
// produce malformed or (worse) unintended JSON.
type tellrawPayload struct {
	RawText []tellrawRun `json:"rawtext"`
}

type tellrawRun struct {
	Text string `json:"text"`
}

// Tell whispers message to the player identified by xuid, by resolving
// their last recorded gamertag from the roster and targeting them with a
// Bedrock name-selector (`@a[name="..."]`), not a bare name token. Bedrock
// gamertags may contain spaces, which a bare name token cannot represent in
// mc-console-bridge's allowlist grammar (a bare token is deliberately
// whitespace-free there, so a player's chat text can never smuggle a second
// console command via an embedded space) — the quoted selector form is
// real Bedrock target-selector syntax and sidesteps that limit entirely.
func (v *BridgeVoice) Tell(ctx context.Context, xuid, message string) error {
	name, ok := v.names.NameFor(xuid)
	if !ok {
		return fmt.Errorf("bridge voice: no known gamertag for xuid %q (never named on any roster), cannot target a tellraw reply", xuid)
	}
	if strings.Contains(name, `"`) {
		// A real Xbox gamertag cannot contain a double quote, but this
		// guards against building a broken (or, worse, differently-scoped)
		// selector out of a name this process didn't validate at the
		// source.
		return fmt.Errorf("bridge voice: gamertag %q for xuid %q contains a double quote, refusing to build a selector from it", name, xuid)
	}

	payload, err := json.Marshal(tellrawPayload{RawText: []tellrawRun{{Text: message}}})
	if err != nil {
		return fmt.Errorf("bridge voice: encode tellraw payload: %w", err)
	}

	target := fmt.Sprintf(`@a[name="%s"]`, name)
	cmd := fmt.Sprintf("tellraw %s %s", target, payload)
	if _, err := v.client.runCommand(ctx, cmd); err != nil {
		return fmt.Errorf("bridge voice: tell %s: %w", xuid, err)
	}
	return nil
}

// sayLineBreaks flattens every line-break form into a single space.
// Bedrock's `say` consumes the rest of the console line, so a message
// carrying a newline would either smuggle a second console line or (as
// mc-console-bridge's allowlist does) be refused outright. Console output
// relayed through Facts is routinely multi-line, so this is the normal
// case, not an edge one.
var sayLineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

// sayNeutralPrefix opens a broadcast whose own first character would
// otherwise decide how the echo of it is read. It is two visible ASCII
// characters rather than a space because the agent's own parser trims
// leading whitespace before looking for CommandPrefix: a space would be
// stripped on the way back in and neutralise nothing. It reads as an aside
// in chat and leaves the rest of the line byte-for-byte intact.
const sayNeutralPrefix = "- "

// commandShapedLead reports whether line opens with a character that lets
// the echo of it be dispatched as a command.
//
// Two characters qualify. CommandPrefix is the obvious one: the echo of a
// reply opening with it is indistinguishable from an operator typing the
// same thing at the console, and is trusted at operator level. A target
// selector is the subtle one -- the server expands `@a` and friends inside a
// `say` message, so a line that *opens* with one has a first character
// chosen by the expansion rather than by the agent, and `@a !op-only` comes
// back as ` !op-only` once the selector resolves to nobody. A selector later
// in the line cannot reach the front and is left alone, which is what keeps
// a reply mentioning @server readable.
func commandShapedLead(line string) bool {
	return strings.HasPrefix(line, chat.CommandPrefix) || strings.HasPrefix(line, "@")
}

// Say broadcasts message to everyone via the console's own `say`, which is
// the entire reason replies go through the bridge rather than a connected
// player's own chat: it carries the console's name rather than a player's.
// The message is flattened to one line first; `say` with nothing left to
// broadcast is an error rather than a silently discarded reply.
//
// A line that would come back command-shaped is prefixed before it leaves,
// so no echo of the agent's own speech can ever parse as a command. This is
// the privilege boundary, not JustSaid below: the console origin is trusted
// at operator level, and every way of recognising an echo after the fact
// fails open somewhere -- an evicted or expired record, a line this process
// never broadcast, a restart mid-flight, text the server rewrote. Refusing
// the reply outright would be the other way to hold the invariant, at the
// cost of dropping an answer a player asked for.
//
// The line is remembered before it is sent, not after: the server can echo
// it back to the agent's own connection before this call returns, and an
// echo that arrives before it is recognisable is one the agent may obey.
func (v *BridgeVoice) Say(ctx context.Context, message string) error {
	line := strings.TrimSpace(sayLineBreaks.Replace(message))
	if line == "" {
		return fmt.Errorf("bridge voice: say: message is empty after flattening line breaks, nothing to broadcast")
	}
	if commandShapedLead(line) {
		line = sayNeutralPrefix + line
	}
	v.remember(line)
	if _, err := v.client.runCommand(ctx, "say "+line); err != nil {
		// A refused command never reached the console, so no echo is coming
		// and the record would otherwise sit there swallowing an operator
		// who happened to type the same line. Only an outright refusal is
		// forgotten: a timeout or a 5xx may have run, and dropping the
		// record on those is how the agent ends up obeying its own reply.
		if refusedBeforeConsole(err) {
			v.forget(line)
		}
		return fmt.Errorf("bridge voice: say: %w", err)
	}
	return nil
}

// JustSaid reports whether line is a broadcast this voice made, consuming
// the record of it.
//
// Everything the agent says in public leaves as a console `say`, and the
// server broadcasts that back to the agent's own connection in the very
// shape an operator typing `say !announce ...` produces — no XUID and the
// console's name. Nothing in the packet separates the two, so the voice that
// said it is the only thing that can.
//
// What this is for is the reply loop: an echo carrying the mention token
// would otherwise be a question the agent asks and answers forever. It is
// deliberately not what stops an echo being run as a command — Say makes
// the line un-command-shaped before it leaves, because every after-the-fact
// match fails open somewhere and a privilege boundary cannot. A miss here
// costs a self-answer the per-actor rate limit bounds, not operator trust.
//
// Consuming the record keeps one echo from vetoing the next: an operator who
// types back exactly what the agent just said is answered, having only lost
// the one occurrence the server already delivered.
func (v *BridgeVoice) JustSaid(line string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.expire()
	return v.take(line)
}

// forget drops the record of line kept for an echo that is not coming.
func (v *BridgeVoice) forget(line string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.take(line)
}

// take removes one record of line and reports whether there was one.
// Callers hold v.mu.
func (v *BridgeVoice) take(line string) bool {
	for i, s := range v.said {
		if s.line == line || (s.echo != nil && s.echo.MatchString(line)) {
			v.said = append(v.said[:i], v.said[i+1:]...)
			return true
		}
	}
	return false
}

func (v *BridgeVoice) remember(line string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.expire()
	if len(v.said) >= sayEchoMemory {
		v.said = v.said[1:]
	}
	v.said = append(v.said, saidLine{line: line, echo: echoPattern(line), at: v.now()})
}

// expire drops records too old to still be awaiting an echo. Callers hold
// v.mu.
func (v *BridgeVoice) expire() {
	cutoff := v.now().Add(-sayEchoWindow)
	kept := v.said[:0]
	for _, s := range v.said {
		if s.at.After(cutoff) {
			kept = append(kept, s)
		}
	}
	v.said = kept
}
