# Crew unit skins

Units join with a generated skin: the outfit comes from the unit's role, the
individual look (skin tone, hair, accent colour) from its name. Same name and
role always give the same skin.

| Role | Outfit |
|---|---|
| miner | yellow hard hat with lamp, grey work shirt, belt |
| chopper (the lumberjack) | plaid flannel in the unit's accent colour, beard |
| hauler | flat cap, tan vest over blue shirt, pack on the back |
| shaft | white helmet, orange hi-vis jacket with reflective bands |
| mender | white coat with a green cross, white cap with a green band |
| builder | white striped cap, tan work apron over an accent-colour shirt, tool belt |
| warrior | iron plate; tabard red (vanguard), purple (striker), green hooded (ranger) |

Armour covers an outfit but not a face or hands, so each unit also has marks
of its own that stay visible under armour: a wristband in its accent colour,
gloves on about half of them (warriors keep their gauntlets), and one of six
face marks (freckles, scar, war paint, heavy brows, beauty mark, clean). A
beard or a full helm covers the face mark, so for choppers and the
vanguard/striker warriors the wristband is the visible mark.

The role comes from `CREW_UNIT_ROLE` (warriors use `CREW_ROLE`). An unset or
unknown role keeps the plain name-coloured skin.

## Changing a skin

```
go run ./minecraft/crew/cmd/crewskin sheet -o roster.png        # preview all outfits
go run ./minecraft/crew/cmd/crewskin export -name Agate -role miner -o Agate.png
```

Edit the exported 64x64 PNG, put it in the directory named by `SKIN_DIR` as
`<unit name>.png`, and the unit wears it from its next join (restart the pod
to apply sooner). A file that is not a 64x64 PNG is logged and ignored. To
change an outfit for everyone, edit `agent/pkg/skin/persona.go` and check the
sheet first.

## AFK bot themes

AFK bots join with a themed skin instead of a flat colour. `SKIN_THEME` picks
the look and `MC_USERNAME` the individual (skin tone, accent colour, face
mark):

| Theme | Look | Used on |
|---|---|---|
| `dotablaze` | charcoal armour, ember trim, flame emblem, burning headband | FWB |
| `sheriff` | one fixed likeness, not per-name: fair, blond, blue-eyed, slim arms, tan Class A shirt with a gold star and tie, green striped trousers, duty belt, campaign hat | FWB's second bot (Dotablaze7321) |
| `neon` (default) | dark hooded jacket, glowing visor and light strips in a per-bot colour | every other server |

Preview with `crewskin sheet -themes -names A,B,C -o themes.png`, export a
starting point with `crewskin export -name A -theme dotablaze -o A.png`, and
override one bot by putting `<MC_USERNAME>.png` in its `SKIN_DIR`.

Every AFK bot also gets its own build and outfit variants, picked from its
name so they stay the same across restarts: wide or slim arms (the sheriff is
always slim), one of three chest marks and one of three head pieces per theme.
