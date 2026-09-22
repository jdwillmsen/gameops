// Copied from github.com/jdwillmsen/minecraft-server-agent/pkg/mcproto.
//
// Copied rather than imported: importing would make the agent's dependency
// floors this bot's, and put every agent release up for review here, to share
// code upstream has stopped changing. docs/decisions.md has the full reasoning.
//
// Fix bugs upstream first, then port here.

// Package mcproto keeps a headless client connectable across Bedrock point
// releases that bump the protocol number without changing the packet schema.
//
// The server hard-kicks any client announcing an older protocol number --
// play_status: failed_client, before login even starts -- so a point release
// shipped as "various bug fixes" with no packet changes still takes every bot
// offline at once.
//
// So this pings the server before dialling and announces whatever number it
// advertises, while still speaking the schema this binary was built against.
// On a release that really does change the schema this trades a clean
// pre-login kick for a parse failure mid-join -- the same reconnect loop
// either way, and the substitution is logged by name so the cause is visible.
//
// docs/protocol-drift.md records the releases this has been needed for.
package mcproto

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sandertv/go-raknet"
	"github.com/sandertv/gophertunnel/minecraft"
)

// pingTimeout bounds the extra delay a ping adds to every connection attempt.
// A server that is mid-restart will not answer, and waiting on it is worse
// than dialling with the compiled-in number and letting the reconnect loop
// handle the failure.
const pingTimeout = 5 * time.Second

// Advertisement is what a server says about itself in its pong.
type Advertisement struct {
	Protocol int32
	Version  string
}

// Negotiate picks the Protocol to dial the server at address with.
//
// It never fails the caller: on any ping or parse problem it returns the
// compiled-in protocol, which is what a client without this package would
// have used. The optional onSpoof callback reports a decision worth logging;
// it is not called when the compiled-in number already matches.
func Negotiate(ctx context.Context, address string, onSpoof func(Advertisement)) minecraft.Protocol {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	pong, err := raknet.PingContext(ctx, address)
	if err != nil {
		return minecraft.DefaultProtocol
	}
	ad, err := ParsePong(string(pong))
	if err != nil {
		return minecraft.DefaultProtocol
	}
	return protocolFor(ad, minecraft.DefaultProtocol, onSpoof)
}

// protocolFor is the decision on its own, so it can be tested without a
// server to ping.
func protocolFor(ad Advertisement, baked minecraft.Protocol, onSpoof func(Advertisement)) minecraft.Protocol {
	if ad.Protocol == baked.ID() {
		return baked
	}
	if onSpoof != nil {
		onSpoof(ad)
	}
	return spoofed{Protocol: baked, id: ad.Protocol}
}

// ParsePong reads the protocol number and version out of a Bedrock pong.
//
// The pong is a semicolon-separated list whose leading fields have been
// stable for years, verified against the production server on 2026-09-08:
//
//	MCPE;FWB Server;2169;1.26.45;4;20;13467591190557326198;FWB;Survival;...
//	 0   1          2    3       4 5  6                    7   8
//
// Only fields 2 and 3 are read. Everything past them varies by server
// software and is none of this package's business.
func ParsePong(pong string) (Advertisement, error) {
	fields := splitPong(pong)
	if len(fields) < 4 {
		return Advertisement{}, fmt.Errorf("mcproto: pong has %d fields, want at least 4", len(fields))
	}
	n, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 32)
	if err != nil {
		return Advertisement{}, fmt.Errorf("mcproto: protocol field %q is not a number", fields[2])
	}
	// A server that advertises 0 or a negative number is broken or is not a
	// Bedrock server at all. Announcing that back would guarantee a kick,
	// where the compiled-in number at least has a chance.
	if n <= 0 {
		return Advertisement{}, fmt.Errorf("mcproto: protocol field is %d, want a positive number", n)
	}
	return Advertisement{Protocol: int32(n), Version: fields[3]}, nil
}

// splitPong splits on unescaped semicolons, matching how the server escapes
// them inside a MOTD.
func splitPong(s string) []string {
	var (
		fields []string
		cur    strings.Builder
		escape bool
	)
	for _, r := range s {
		switch {
		case escape:
			escape = false
			cur.WriteRune(r)
		case r == '\\':
			escape = true
		case r == ';':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(fields, cur.String())
}

// spoofed announces a different protocol number while delegating everything
// that touches packet layout to the protocol this binary was built against.
//
// Overriding ID alone is the whole point: change anything else and the client
// would be claiming to speak a schema it does not have.
type spoofed struct {
	minecraft.Protocol
	id int32
}

func (s spoofed) ID() int32 { return s.id }
