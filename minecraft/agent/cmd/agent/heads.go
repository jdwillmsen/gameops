package main

import (
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/heads"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/skin"
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

// hairTint is the piece type a skin's hair colour is filed under.
const hairTint = "persona_hair"

func headSkin(s protocol.Skin) heads.Skin {
	animations := make([]heads.Animation, len(s.Animations))
	for i, a := range s.Animations {
		animations[i] = heads.Animation{
			Type:       a.AnimationType,
			Expression: a.ExpressionType,
			Width:      a.ImageWidth,
			Height:     a.ImageHeight,
			Frames:     a.FrameCount,
			Bytes:      len(a.ImageData),
		}
	}
	tints := skin.Tints{Skin: s.SkinColour}
	made := heads.Made{ID: s.SkinID, Premium: s.PremiumSkin, Hashed: s.ProfileHash != ""}
	for _, p := range s.PersonaPieces {
		made.Pieces = append(made.Pieces, p.PieceType)
	}
	for _, t := range s.PieceTintColours {
		made.Tinted = append(made.Tinted, t.PieceType)
		if t.PieceType == hairTint {
			tints.Hair = t.Colours[0]
		}
	}
	return heads.Skin{
		Width:         s.SkinImageWidth,
		Height:        s.SkinImageHeight,
		Data:          s.SkinData,
		Persona:       s.PersonaSkin,
		ResourcePatch: s.SkinResourcePatch,
		Geometry:      s.SkinGeometry,
		Animations:    animations,
		Tints:         tints,
		Made:          made,
	}
}
