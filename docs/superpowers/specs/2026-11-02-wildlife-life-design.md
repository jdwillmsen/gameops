# Wildlife life cycle: drops, tamed look, breeding, armor

Owner's asks (2026-11-02), on the proving-grounds server first. Choices made: per-species litters and cubs,
armor for the animals, collar texture plus hearts and a sound on taming.

## Stages (each ships on its own: pack PR, lab-packs image, tag bump on proving-grounds)

1. **Drops, XP, taming feedback, owner goal fix** (this PR). Loot tables from the species table; XP only for a
   player kill; hearts and a chime on `gameops:on_tamed`; `owner_hit_target` renamed to the valid
   `owner_hurt_target` (the old name was ignored, so tamed animals never joined the owner's attacks).
2. **Collar.** Entity property `gameops:tamed` set by `on_tamed`; render controller picks a tamed texture per
   variant (variant x tamed array). Each species gets a collar band painted in its own UV neck region. Risk:
   UV overlap on the cat rigs; keep the band on its own cube face and verify with the Blockbench preview tool.
3. **Breeding.** Tamed adults fed their taming food breed (`minecraft:breedable`). Species differ by litter
   size and growth time: tiger 1-2, cheetah 2-3, lion 1-3 (random sex), leopard 1-2, bear 1 (slow), hyena 2-4.
   Babies are half size (collision box and render scale by `query.is_baby`), grow up on a timer, and are tamed
   to the parent's owner (copy `gameops:owner` from the nearest tamed parent in the spawn handler). Open
   question to spike on BDS: whether a custom entity's breedable component supports multiple births, else the
   script adds the extra cubs.
4. **Armor for the animals.** Three tiers (leather, iron, diamond) as custom items equipped by using the item on
   a tamed animal (`EntityEquippableComponent` if BDS allows it on custom mobs, else a dynamic property),
   damage reduction applied by the script, and a model overlay per rig (cat, bear, hyena) shown through a
   render-controller part visibility keyed on a property. Biggest piece; needs its own spike first.
5. **Pelts** (optional, ties 1 and 4 together): a rare custom drop used to craft the armor.

## Constraints

- Stable names: component groups, events and properties already in worlds must not change; stages add only.
- Bump both pack versions and the dependency each time (clients cache by UUID and version).
- The collar and armor art cannot be judged headlessly; the owner's eyes are the final check.
