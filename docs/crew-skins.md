# Crew unit skins

Units join with a generated skin: the outfit comes from the unit's role, the
individual look (skin tone, hair, accent colour) from its name. Same name and
role always give the same skin.

| Role | Outfit |
|---|---|
| miner | yellow hard hat with lamp, grey work shirt, belt |
| chopper | plaid flannel in the unit's accent colour, beard |
| hauler | flat cap, tan vest over blue shirt, pack on the back |
| shaft | white helmet, orange hi-vis jacket with reflective bands |
| warrior | iron plate; tabard red (vanguard), purple (striker), green hooded (ranger) |

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
