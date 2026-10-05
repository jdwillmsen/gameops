# FWB Minecraft Jira board: design

Date: 2026-10-05
Status: awaiting review

## Purpose

Give the three FWB players one place to track what they are building and
aiming for in the world, see it progress, and capture ideas as they come up.

The board covers the game side only: builds, goals, chores, events, ideas and
in-game problems. The software that runs the server (this repo, the platform
tenant, alerts) keeps its existing tracker.

Success looks like this: any of the three can add a card in a few seconds,
the board shows at a glance what is in flight and what is stalled, and the
Done column reads as a history of the world.

## Decisions

### A separate Atlassian site

The board lives on a new free site, `fwb-minecraft.atlassian.net`, not on
`jdwillmsen.atlassian.net`.

The two casual players must not be able to see the `JDWLABS` or `CAREER`
spaces. Jira's Free plan cannot restrict who sees a space: space permissions
and roles are editable only from the Standard plan up. A separate site makes
the isolation structural at no cost, and it means any credential later issued
for in-game intake can reach FWB cards and nothing else.

What this costs:

- FWB cards do not appear in any cross-space view on the main site.
- A link from an FWB card to a `JDWLABS` ticket is a plain URL the other
  players cannot open.
- There is a second site to sign in to.

Alternatives rejected: upgrading the main site to Standard (about 9 USD per
user per month, figure not confirmed against the pricing page), and setting
spaces to Private during a Standard trial and then downgrading, which leaves
every space created afterwards open again.

### One team-managed kanban space

- Name: FWB Minecraft
- Key: `FWB`
- Type: team-managed software space, kanban template
- Members: all three players, as full members

Team-managed keeps the work types and fields local to the space and allows up
to 30 custom work types, each with its own workflow. It also allows exactly
one board, which is all this needs.

Kanban over sprints: a hobby server has no steady capacity, so time boxes
would only produce rollover.

### Work types

| Type    | What it is                                              | Example                    |
|---------|---------------------------------------------------------|----------------------------|
| Goal    | A long-running aim that other cards roll up under       | Full beacon                |
| Build   | A structure, farm or piece of infrastructure            | Iron farm at spawn         |
| Task    | Gathering, exploration, events, chores                  | Map the nearest stronghold |
| Idea    | Anything not yet agreed                                 | Glass dome over the bay    |
| Problem | Something broken or annoying in-game                    | Villagers escaping the hall|

Goal is the space's Epic type, renamed. If Jira does not allow renaming it,
it stays "Epic" and is used the same way.

Farms, nether hubs, redstone and storage are not separate types. They follow
the same workflow as any Build, so they are a Category value.

### Board columns

`Ideas → Next Up → In Progress → On Hold → Done`

- **Ideas**: uncommitted. Anything can be dropped here.
- **Next Up**: agreed, not started.
- **In Progress**: someone is actively on it.
- **On Hold**: started and paused. Long builds stall; this keeps In Progress
  honest without pretending the build is abandoned.
- **Done**: finished. In a permanent world this never resets, so it doubles
  as the record of what has been built.

No column limits. All five types share these columns.

A card that will not happen is closed with a Won't Do resolution, not given
its own column.

### Fields

| Field    | Kind         | Values                                                                                |
|----------|--------------|---------------------------------------------------------------------------------------|
| Category | Single select| Megabuild, Farm, Infrastructure, Redstone, Exploration, Gathering, Event, Server      |
| Location | Short text   | One line, coordinates then dimension: `-240 64 1180, Overworld`                       |
| Assignee | Built in     | Who is on it                                                                          |

Only the summary is required on any type.

Conventions that use built-in features, not fields:

- A finished Build gets a screenshot attached.
- A prerequisite ("needs an iron farm first") is a "blocked by" link.
- Materials for a large build are a checklist in the description.
- Progress on a Goal comes from its child cards, not a percentage anyone
  maintains.

Deliberately left out because nobody keeps them current: progress percent,
size or difficulty, biome, and a builder field separate from assignee.

### Phases

The world is permanent, so there are no seasons. Named eras of the world
("Nether hub era") are Jira versions on the space. None are created at
launch; the first is added when the players name one.

### Starting content

The board starts empty. Cards go in as ideas and work come up.

### Link to engineering

Software work stays in `JDWLABS` on the main site. When an Idea or Problem
needs code, a `JDWLABS` ticket is filed and its URL is pasted on the FWB
card. The FWB card stays open until the players see the result in-game.

## Setup

Split by who can do each step.

**Owner, in the browser**

1. Create the site `fwb-minecraft.atlassian.net` on the Free plan.
2. Create the space: team-managed, kanban, name "FWB Minecraft", key `FWB`.
3. Configure work types, columns and fields as above, from a click-by-click
   checklist prepared alongside the implementation plan.
4. Invite the other two players.

**Agent**

1. Before the owner starts step 3, test whether the Jira REST API can create
   work types, fields or columns in a team-managed space. Research found no
   public endpoint for this and could not read the API reference in full, so
   it is unconfirmed either way. Whatever the API can do is scripted;
   whatever it cannot stays on the checklist.
2. Once the Atlassian connector is authorised for the new site, read back
   the space configuration and compare it with this spec.

## Verification

- The space has the five work types, five columns in order, and the Category
  and Location fields with the listed values.
- A card of each type can be created with only a summary.
- Each of the three players can create a card and move it between columns.
- Signed in as one of the two casual players, the main site's `JDWLABS` and
  `CAREER` spaces are not reachable. They hold no account on that site, so
  this confirms nothing was shared by mistake.

## Out of scope

Each of these is a separate piece of work with its own design.

- **In-game `!idea` command** that files an Idea card through the server
  agent. First follow-up after the board has had two weeks of use; filed as a
  `JDWLABS` ticket then. Open questions it carries: how the agent holds a
  Jira credential, spam and rate limits, and which player a card is
  attributed to.
- **Regrouping `JDWLABS`** into Platform, GameOps and Career spaces with a
  cross-space overview board for the owner.
- **A digest** of board activity to the players' group chat.
- **Map waypoints** linked to Build cards.

## Evidence and its limits

- Free-plan permissions: Atlassian support documentation states that space
  permissions and roles cannot be edited on the Free plan.
- Team-managed limits (30 work types, one board, space-local fields):
  Atlassian support documentation.
- Types, columns and fields: no write-ups from real survival groups were
  found. The shortlist is inferred from commercial planner templates, one
  plugin built to replace a pinned chat message, and general kanban guidance.
  Expect to adjust the Category values and the On Hold column after real use.
