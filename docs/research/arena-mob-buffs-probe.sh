#!/usr/bin/env bash
# Arena mob-buff console-command probe.
# Battery behind docs/research/2026-10-23-arena-mob-buffs.md. Run against a
# throwaway BDS container, never a lab:
#
#   docker run -d --name arena-spike-bds -e EULA=TRUE -e ALLOW_CHEATS=true \
#     -e ONLINE_MODE=false -e ALLOW_LIST=false -e GAMEMODE=creative \
#     -p 127.0.0.1:19235:19132/udp itzg/minecraft-bedrock-server:2026.10.1
#   CONTAINER=arena-spike-bds ./arena-mob-buffs-probe.sh
#
# ALLOW_LIST=false is required or BDS refuses to listen. Responses come back
# in `docker logs`, not on the send-command pipe.
set -euo pipefail
CONTAINER="${CONTAINER:-arena-spike-bds}"

probe() {
  docker exec "$CONTAINER" send-command "$1"
  sleep 0.7
  docker logs --since 2s "$CONTAINER" 2>&1 \
    | sed 's/^\[[^]]*INFO\] /I /; s/^\[[^]]*ERROR\] /E /' \
    | grep -E '^(I|E) ' || true
}

echo '== ticking area (no spawns without this) =='
probe 'tickingarea add -16 60 -16 16 60 16 arena'

echo '== spawn + name + tag + count =='
probe 'summon zombie "ProbeZ" 0 126 0'
probe 'testfor @e[name=ProbeZ]'
probe 'tag @e[name=ProbeZ] add arena_wave'
probe 'tag @e[tag=arena_wave] list'
probe 'testfor @e[type=!item]'

echo '== spawn events (rotations required; bad token -> syntax error) =='
probe 'summon zombie 4 126 4 0 0 minecraft:become_drowned "DrownedElite"'
probe 'summon creeper 8 126 8 0 0 minecraft:charged_creeper "ChargedBoom"'
probe 'summon pillager 12 126 12 0 0 minecraft:promote_to_patrol_captain "Captain"'

echo '== effects (hp proof via damage; baseline zombie dies at 30) =='
probe 'summon zombie 16 126 16'
probe 'effect give @e[type=zombie,c=1] resistance 600 4'
probe 'damage @e[type=zombie,c=1] 30'
probe 'testfor @e[type=zombie]'

echo '== gear + enchant (mainhand only) =='
probe 'summon zombie "Armed" 20 126 20'
probe 'replaceitem entity @e[name=Armed] slot.weapon.mainhand 0 iron_sword 1'
probe '/enchant @e[name=Armed] sharpness 5'
probe 'replaceitem entity @e[name=Armed] slot.armor.head 0 diamond_helmet 1'

echo '== boss summons + cleanup =='
probe 'summon ravager 0 130 0'
probe 'testfor @e[type=ravager]'
probe 'execute if entity @e[type=ravager] run summon zombie 24 126 24'
probe 'kill @e[type=!player]'
probe 'tickingarea remove arena'
