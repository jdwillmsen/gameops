-- The crew unit event stream: the durable counterpart of the console's
-- fleet feed.
--
-- The feed lives in memory, 200 events deep, and is lost on every restart;
-- history cannot be filtered, sorted or replayed. crew_event is the
-- append-only record of what a unit did and what happened to it, written by
-- crew-control as it derives events from polling the units.
--
-- Repeated idle polls are not one row each: a unit that finds nothing to do
-- many times a minute is stored as one idle_rollup row whose payload holds
-- the count and the last poll's time. Without that rule most of the table
-- would be no-ops.
--
-- kind is deliberately not a CHECK: the list grows, and a constraint would
-- need a migration per addition. crew-control owns the vocabulary and the
-- store writes only what it accepted. severity is constrained, because the
-- console's colouring and toasts have a fixed vocabulary.
--
-- unit is the crew registry slug, not a foreign key, like crew_work's: a unit
-- must be addressable before it has ever connected. task_id and job_id are not
-- foreign keys either, for the same reason as crew_job_step.work_id: events
-- must outlive, or predate, the rows they name.
--
-- at is timestamptz, so the instant is stored UTC and read back in whatever
-- zone the console asks for. x, y and z are nullable: an offline event happens
-- nowhere in particular. payload is a JSON object for the kind's own detail.
--
-- All objects live in the existing minecraft schema on purpose: V2's default
-- privileges for app cover that schema, so no grant is needed here.
CREATE TABLE IF NOT EXISTS minecraft.crew_event
(
    id       BIGINT           NOT NULL GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    unit     TEXT             NOT NULL,
    at       TIMESTAMPTZ      NOT NULL DEFAULT now(),
    kind     TEXT             NOT NULL,
    severity TEXT             NOT NULL CHECK (severity IN ('ok', 'info', 'warn', 'crit')),
    task_id  UUID,
    job_id   UUID,
    x        DOUBLE PRECISION,
    y        DOUBLE PRECISION,
    z        DOUBLE PRECISION,
    payload  JSONB            NOT NULL DEFAULT '{}'::jsonb
);

-- One index per console query shape: a unit's history, a kind across the
-- fleet, one severity's slice, and the unfiltered stream (which also serves
-- the retention delete). Each ends in (at, id) because the query pages by
-- keyset on that pair: the cursor comparison and the ORDER BY are then both
-- answered by one index scan in either direction, with no sort step.
CREATE INDEX IF NOT EXISTS crew_event_unit_at_idx
    ON minecraft.crew_event (unit, at, id);
CREATE INDEX IF NOT EXISTS crew_event_kind_at_idx
    ON minecraft.crew_event (kind, at, id);
CREATE INDEX IF NOT EXISTS crew_event_severity_at_idx
    ON minecraft.crew_event (severity, at, id);
CREATE INDEX IF NOT EXISTS crew_event_at_idx
    ON minecraft.crew_event (at, id);
