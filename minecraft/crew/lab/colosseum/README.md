# Colosseum

The crew lab's second arena, centred x -300, z -758 on flat grass west of the
labcourses arena (which stands at x 0, z -760 and is left alone). `colosseum.py`
builds it as console commands; `arena.sh --colosseum` feeds them to the server
and runs the director's waves against it.

```
arena.sh --colosseum --confirm-overwrite build k8s:<ns>/<pod>   # clear and rebuild the whole box
arena.sh --colosseum --confirm-overwrite run   k8s:<ns>/<pod> --tier 2 --warriors Rusty,Slate,Tinder
```

`--centre=X,Z` moves it. The build replaces everything in x -420..-181,
z -864..-653, y 36..122 and takes about 30 minutes: the console runs about 20
commands a second. Before a rebuild save the box as 48-block `structure save`
tiles so it can be put back.
`colosseum.py OUT --against=OLD.npy --save=NEW.npy` writes only `build.cmds` with
what differs from an earlier `--save` (matched by material name, so adding a
material is safe), for patching a built arena in place: feed that file alone,
not `plan.txt`, and expect a full build to clear and re-dress the armour stands.

## What is there

| Part | Notes |
|---|---|
| Sand floor | 120 x 88 ellipse with ring bands and a gold medallion |
| Stands | four tiers, aisles every fourth bay, lanterns on the aisles |
| Outer wall | four arcaded storeys and an attic, 80 bays, banners, masts |
| Gates | four, on the axes; each is a portcullis of iron bars. The north one is the squad's start gate (the director opens it with `fill ... air`). The other three have a double iron door set in the bars and a lever either side of it |
| Spawn pens | twelve, under the stands, each a straight 3 x 3 gate of iron bars the director opens and shuts. The generator refuses to write a build in which a pen could reach the arena with its gate shut |
| Royal box | gold-trimmed canopy over the south stands, a throne, and a fanfare of noteblocks on one button |
| Wall-top lights | a runway of redstone lamps along the top of the wall, one lever, a repeater every ten cells; the signal ripples round. Four ladders up the inside of the wall reach it |
| Plaza | paving, lamp posts, four fountains, colonnaded avenues |

Underground, floor y 49:

| Part | Notes |
|---|---|
| Hall of champions | 80 x 48, columns, chandeliers, a centre fountain, a stand of every gear tier on a plinth, four lamp arms with a lever each |
| Water lifts | four glass tanks with a soul-sand base carry a mob up to an iron hatch in the arena floor; a lever on the pedestal beside each hatch opens it |
| Ring corridor | round the hall, arches in |
| Sixteen chambers | forge, armoury, gear selection, library, barracks, infirmary, trophy hall, treasury (iron door), operations room, baths, kitchen, shrine, beast cages, cells, storage, lounge. Chests are stocked and armour stands dressed by `decor.cmds` |
| Four tunnels | from the ring out under the avenues, climbing a stair to the plaza |

## Controls

Lamp arms, the wall-top ring and the ops-room banks are levers and dust. The
hatches open with the lever on the pedestal beside each. Levers and buttons
need a player, so crew units cannot work them; the director uses `fill`.

## Making changes

- Mobs and warriors use `layout.json`, which `colosseum.test.sh` keeps equal to
  the generator's output. After a change run the generator and copy its
  `layout.json` here.
- Bedrock facts the generator relies on, checked on the lab server: piston
  `facing_direction` 0 down, 1 up, 2 +z, 3 -z, 4 +x, 5 -x; a repeater's
  `cardinal_direction` names its input side; `/fill` ... `redstone_wire` needs a
  block below it, so attachables and fluids are placed after every solid block.
  Stairs ascend toward the direction `ASC` maps; set `COLO_FLIP_STAIRS=1` if a
  build shows them reversed.
