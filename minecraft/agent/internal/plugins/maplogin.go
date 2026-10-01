package plugins

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/chat"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/plugin"
)

// MapClaimer tells the map service that a player typed a login code, or
// wants every map login of theirs ended.
type MapClaimer interface {
	Claim(ctx context.Context, code, xuid, gamertag string) error
	Revoke(ctx context.Context, xuid string) error
}

// MapNames resolves an XUID to the gamertag the map page will show.
type MapNames interface {
	NameFor(xuid string) (name string, ok bool)
}

// mapCode is the shape the map service issues: six characters from an
// alphabet without 0, O, 1 or I. Checking it here keeps typos from being
// spent as guesses against the service.
var mapCode = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{6}$`)

// MapLogin is how a player proves to the world map that they are on the
// server. The map page shows a code; typing it in chat is something only a
// connected player can do, and the chat packet carries their XUID. Links in
// Bedrock chat cannot be clicked on a console or a phone, which is why the
// code travels from the browser to the game and not the other way.
type MapLogin struct {
	claimer MapClaimer
	names   MapNames
	url     string
}

// NewMapLogin builds the plugin. url is the map's public address, quoted in
// replies so a player knows where the code comes from.
func NewMapLogin(claimer MapClaimer, names MapNames, url string) *MapLogin {
	return &MapLogin{claimer: claimer, names: names, url: url}
}

func (*MapLogin) Name() string { return "map" }

func (m *MapLogin) Commands() []plugin.Command {
	return []plugin.Command{
		{
			Name:        "map",
			Description: "Unlock the world map in your browser: !map <code>. !map logout ends your map logins.",
			// Visitor: being able to chat is the whole proof the map asks
			// for, and everyone on the server can.
			Permission: plugin.PermissionVisitor,
			Run:        m.run,
		},
	}
}

func (m *MapLogin) run(ctx context.Context, _ *plugin.Context, inv plugin.Invocation) (string, error) {
	if len(inv.Args) == 0 {
		return fmt.Sprintf("The world map is at %s. Open it, then type !map and the code it shows you. Only type a code from a page you opened yourself.", m.url), nil
	}
	// The console can speak in chat, but a map login belongs to a player.
	if inv.ActorXUID == chat.ServerOrigin {
		return "Only a player in the game can use !map.", nil
	}
	if len(inv.Args) == 1 && strings.EqualFold(inv.Args[0], "logout") {
		if err := m.claimer.Revoke(ctx, inv.ActorXUID); err != nil {
			return "", fmt.Errorf("map: !map logout: %w", err)
		}
		return "Every browser that was logged in to the map as you has been logged out.", nil
	}
	code := strings.ToUpper(inv.Args[0])
	if len(inv.Args) != 1 || !mapCode.MatchString(code) {
		return fmt.Sprintf("A map code is six letters and digits, like !map ABC234. Open %s to get one.", m.url), nil
	}
	gamertag, _ := m.names.NameFor(inv.ActorXUID)
	err := m.claimer.Claim(ctx, code, inv.ActorXUID, gamertag)
	switch {
	case errors.Is(err, mapclient.ErrUnknownCode):
		return fmt.Sprintf("That code is not valid or has expired. Open %s for a new one.", m.url), nil
	case err != nil:
		return "", fmt.Errorf("map: !map: %w", err)
	}
	// Someone talked into typing a code off another person's screen sees
	// only this reply, so the way to undo it has to be here.
	return "The map is unlocked in the browser that showed that code. If that was not your own screen, type !map logout.", nil
}
