// Package chat turns raw Bedrock text packets into agent-relevant facts:
// who sent it (by XUID, never by the spoofable gamertag), whether it's
// something the agent should react to (an @server mention or a ! command),
// and whether it should be ignored as our own echo or a sibling bot's.
package chat

import (
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// ServerOrigin is the sentinel identity used for console-originated messages
// (e.g. `send-command say ...`), which arrive with no XUID.
const ServerOrigin = "<server>"

// ConsoleSourceName is the name Bedrock puts on the announcement it
// broadcasts for a console `say`. It is the server's own fixed word for the
// console, not its configured server-name, and the message repeats it as a
// "[Server] " prefix.
const ConsoleSourceName = "Server"

// CommandPrefix marks a chat line as a bot command, e.g. "!help".
const CommandPrefix = "!"

// MentionToken is the case-insensitive substring that triggers an @server
// answer, e.g. "@server how do I craft a beacon".
const MentionToken = "@server"

// answerableTypes are the Text packet types that represent a message a real
// person could have typed, as opposed to popups/tips/system text.
var answerableTypes = map[byte]struct{}{
	packet.TextTypeChat:         {},
	packet.TextTypeWhisper:      {},
	packet.TextTypeAnnouncement: {},
}

// IsAnswerableType reports whether textType is one the agent should ever
// look at for commands or mentions.
func IsAnswerableType(textType byte) bool {
	_, ok := answerableTypes[textType]
	return ok
}

// privateTypes are the answerable types that reached the agent without an
// audience. Only the whisper qualifies: Bedrock's /tell (and its /msg and /w
// aliases) is delivered to the recipient alone, so nobody else saw the
// question asked.
//
// TextTypeObjectWhisper is deliberately absent. That is what a console
// tellraw arrives as, not a player's /tell, and it is not answerable in the
// first place -- adding it here would classify as private a message the
// agent never reads.
var privateTypes = map[byte]struct{}{
	packet.TextTypeWhisper: {},
}

// IsPrivateType reports whether a message of this type was seen by anyone
// but its sender and the agent. It decides where an @server answer goes: the
// broadcast default exists because an answer only the asker can see reads as
// no answer at all to the rest of the chat that watched them ask, and a
// whisper has no such audience to answer to.
func IsPrivateType(textType byte) bool {
	_, ok := privateTypes[textType]
	return ok
}

// IsPublicType reports whether textType is something every connected player
// saw. A whisper is answerable but not public: only the agent received it,
// so it is nobody else's business what it said.
func IsPublicType(textType byte) bool {
	return textType == packet.TextTypeChat || textType == packet.TextTypeAnnouncement
}

// Identity resolves the XUID-based identity of a Text packet's sender.
//
// A real player always carries a non-empty XUID on chat/whisper/announcement
// packets, so that XUID is the identity — SourceName is never trusted for
// identity because it is attacker-controlled (a player can set an arbitrary
// display name).
//
// A console-originated message carries no XUID. The empty XUID is what
// carries the trust, not the name beside it: a connected player's chat is
// stamped with their XUID by the server, so no line a player can send ever
// reaches this branch. A console `say` arrives named ConsoleSourceName and
// an unnamed server line arrives with no name at all; both resolve to
// ServerOrigin.
//
// Any other name over an empty XUID stays rejected rather than trusted. An
// operator's own `/say` reaches the agent under the operator's gamertag with
// no XUID to bind it to, and granting the console sentinel to a line
// identified only by a display name is the impersonation this guards.
func Identity(pk *packet.Text) (id string, ok bool) {
	if pk.XUID != "" {
		return pk.XUID, true
	}
	if pk.SourceName == "" || pk.SourceName == ConsoleSourceName {
		return ServerOrigin, true
	}
	return "", false
}

// Body returns the line to read a trigger from.
//
// Bedrock broadcasts a console `say` as an announcement whose message
// repeats the source name in brackets — `[Server] !announce ...` — so the
// raw message never starts with CommandPrefix and no console command ever
// matched. Only a server-generated line (no XUID) is unwrapped: a player
// carries an XUID, so nobody can shed a bracketed prefix of their own
// choosing and have the remainder dispatched.
func Body(pk *packet.Text) string {
	if pk.XUID != "" || pk.SourceName == "" {
		return pk.Message
	}
	return strings.TrimPrefix(pk.Message, "["+pk.SourceName+"] ")
}

// IsSelfOrSibling reports whether id belongs to this bot or a sibling bot,
// so the agent never reacts to its own messages or another bot's — the only
// defence against an infinite reply loop when multiple bots occupy the
// server.
func IsSelfOrSibling(id, selfXUID string, siblingXUIDs map[string]struct{}) bool {
	if id == selfXUID {
		return true
	}
	_, sibling := siblingXUIDs[id]
	return sibling
}

// TriggerKind classifies what, if anything, a chat message asks the agent
// to do.
type TriggerKind int

const (
	// TriggerNone means the message is ordinary player conversation.
	TriggerNone TriggerKind = iota
	// TriggerCommand means the message starts with CommandPrefix.
	TriggerCommand
	// TriggerMention means the message contains MentionToken.
	TriggerMention
)

// Trigger is the result of inspecting a chat message for something the
// agent should react to.
type Trigger struct {
	Kind TriggerKind
	// Command and Args are set only when Kind == TriggerCommand. Command is
	// lowercased and has CommandPrefix stripped; Args is the remaining
	// whitespace-split tokens, unmodified.
	Command string
	Args    []string
	// Message is set when Kind == TriggerMention: the original message,
	// unmodified, for the LLM to read as the question.
	Message string
}

// ParseTrigger inspects a chat message and classifies it. Command detection
// takes priority over mention detection: "!ask @server ..." is a command,
// not a mention.
func ParseTrigger(message string) Trigger {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return Trigger{Kind: TriggerNone}
	}

	if strings.HasPrefix(trimmed, CommandPrefix) {
		body := strings.TrimPrefix(trimmed, CommandPrefix)
		fields := strings.Fields(body)
		if len(fields) == 0 {
			return Trigger{Kind: TriggerNone}
		}
		return Trigger{
			Kind:    TriggerCommand,
			Command: strings.ToLower(fields[0]),
			Args:    fields[1:],
		}
	}

	if strings.Contains(strings.ToLower(trimmed), MentionToken) {
		return Trigger{Kind: TriggerMention, Message: trimmed}
	}

	return Trigger{Kind: TriggerNone}
}

// MessageKind is the bus event kind published for every answerable chat
// message that survives identity resolution and the self/sibling guard.
const MessageKind = "chat.message"

// MessageEvent is what the protocol client publishes on the bus for each
// such message, so event-driven plugins can observe chat without touching
// the Bedrock connection.
type MessageEvent struct {
	// ActorXUID is the resolved sender identity (see Identity).
	ActorXUID string
	// Gamertag is ActorXUID's name from the live roster, or the XUID itself
	// when the roster cannot name them. Never the packet's SourceName, which
	// the sender controls.
	Gamertag string
	// Message is the chat line as Body returned it: unmodified for a player,
	// and stripped of the console's bracketed prefix for a server line, so
	// what a handler reads is what Trigger was classified from.
	Message string
	// Trigger is the classification ParseTrigger produced for Message.
	Trigger Trigger
	// Public reports whether everyone connected saw Message (IsPublicType).
	Public bool
}

// Kind satisfies the bus's Event interface.
func (MessageEvent) Kind() string { return MessageKind }
