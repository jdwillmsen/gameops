package main

import (
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/heads"
)

// headsTimeout bounds one report of player heads to the map. Nothing waits
// on it but the next report.
const headsTimeout = 5 * time.Second

// watchHeads passes on the two packets that carry skins: the player list,
// which has everyone's when they join, and the change of skin a player
// makes while online.
func watchHeads(pk packet.Packet, w *heads.Watch) {
	switch pk := pk.(type) {
	case *packet.PlayerList:
		entries := make([]heads.Entry, len(pk.Entries))
		for i, e := range pk.Entries {
			entries[i] = heads.Entry{
				UUID:   e.UUID.String(),
				XUID:   e.XUID,
				Name:   e.Username,
				Remove: e.ActionType == protocol.PlayerListActionRemove,
				Skin:   headSkin(e.Skin),
			}
		}
		w.List(entries)
	case *packet.PlayerSkin:
		w.Reskin(pk.UUID.String(), headSkin(pk.Skin))
	}
}

func headSkin(s protocol.Skin) heads.Skin {
	return heads.Skin{
		Width:         s.SkinImageWidth,
		Height:        s.SkinImageHeight,
		Data:          s.SkinData,
		Persona:       s.PersonaSkin,
		ResourcePatch: s.SkinResourcePatch,
	}
}
