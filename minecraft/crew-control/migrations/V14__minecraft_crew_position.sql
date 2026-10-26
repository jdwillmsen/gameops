-- Where units stood, sampled coarsely, so the console can replay a past
-- window on a top-down map.
--
-- Separate from crew_event on purpose: a position every couple of seconds per
-- moving unit would outnumber every other event kind many times over and slow
-- the explorer, the analytics and the retention delete that share that table.
-- crew-control stores a row only when a unit moved or after a keepalive, and
-- the console interpolates between rows.
--
-- unit is the crew registry slug, not a foreign key, like crew_event's. Only
-- the overworld is recorded, so there is no dimension column. Rows are
-- operator-only data: no route that serves them to a signed-out reader exists.
-- Retention matches crew_event's and is enforced by crew-control's prune job.
--
-- All objects live in the existing minecraft schema, so V2's default
-- privileges for app already cover them.
CREATE TABLE IF NOT EXISTS minecraft.crew_position
(
    id   BIGINT           NOT NULL GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    unit TEXT             NOT NULL,
    at   TIMESTAMPTZ      NOT NULL,
    x    DOUBLE PRECISION NOT NULL,
    y    DOUBLE PRECISION NOT NULL,
    z    DOUBLE PRECISION NOT NULL
);

-- Both read shapes page by keyset on (at, id): the whole fleet over a range,
-- and one unit over a range. The first also serves the retention delete.
CREATE INDEX IF NOT EXISTS crew_position_at_idx
    ON minecraft.crew_position (at, id);
CREATE INDEX IF NOT EXISTS crew_position_unit_at_idx
    ON minecraft.crew_position (unit, at, id);
