# shellcheck shell=bash disable=SC2034
# Layout constants shared by parallel-scene.sh and parallel-demo.sh. Sourced,
# not run. Everything is at y=100 on a smooth_stone floor at y=99, inside the
# cleared box x -12..12, y 100..106, z -12..12.

AREA_MIN=(-12 99 -12)
AREA_MAX=(12 106 12)

# Stone for Agate; 24 blocks, so a 16-block gather has slack.
QUARRY_FROM=(6 100 -10)
QUARRY_TO=(9 101 -8)

# Oak trunks, two logs high, for Basalt. Ten trunks (20 logs) for a 16-log
# gather: with exactly 16 the unit came up one log short.
GROVE_X=(-9 -7 -5 -3 -1)
GROVE_Z=(8 10)

# Chest positions as "x y z".
OUTBOX_A=(4 100 -5)
OUTBOX_B=(-5 100 5)
STORE=(0 100 0)
# Stocked with swords and bread for units that set KIT_CHEST to this
# position. Clear of the quarry, the grove and the other chests.
KIT_CHEST=(-3 100 -3)
KIT_SWORDS=6
KIT_BREAD=64

START_AGATE=(5 100 -7)
START_BASALT=(-6 100 7)
START_COBBLE=(1 100 2)

DEFAULT_NAMES=(Agate Basalt Cobble)
