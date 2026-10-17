# FWB Minecraft Jira board: design

Date: 2026-10-05
Status: built 2026-10-06; player invites and their checks outstanding

## Purpose

Give the three FWB players one place to track what they are building and
aiming for in the world, see it progress, and capture ideas as they come up.

The board covers the game side only: builds, goals, chores, events, ideas and
in-game problems. The software that runs the server (this repo, the platform
tenant, alerts) keeps its existing tracker.

Success looks like this: any of the three can add a card in a few seconds,
the board shows at a glance what is in flight and what is stalled, and the
list of finished cards reads as a history of the world.

## Decisions

### A separate Atlassian site

The board lives on a new free site, `fwb-minecraft.atlassian.net`, not on
`jdwillmsen.atlassian.net`.

The two casual players must not be able to see the `JDWLABS` or `CAREER`
spaces. Jira's Free plan cannot restrict who sees a space: space permissions
and roles are editable only from the Standard plan up. A separate site makes
the isolation structural at no cost.

The separate site does not by itself confine a credential. An Atlassian API
token belongs to an account and works on every site that account can reach,
so a token minted from the owner's account would open the main site too. Any
credential issued for in-game intake therefore belongs to a dedicated account
that is a member of this site only.

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
to 30 work types in total, each with its own workflow. It comes with a single
board of its own, which is all this needs.

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

Goal is the space's Epic type, renamed. Jira's built-in Subtask type is
also present; it cannot be removed and is not part of the design.

Farms, nether hubs, redstone and storage are not separate types. They follow
the same workflow as any Build, so they are a Category value.

### Board columns

`Ideas → Next Up → In Progress → On Hold → Done`

- **Ideas**: uncommitted. Anything can be dropped here.
- **Next Up**: agreed, not started.
- **In Progress**: someone is actively on it.
- **On Hold**: started and paused. Long builds stall; this keeps In Progress
  honest without pretending the build is abandoned.
- **Done**: finished. The column shows recent finishes only: Jira clears a
  card from a team-managed board 14 days after it lands here. Nothing is
  deleted. The lasting record of what has been built is the full list of
  done cards, reached from the column's "See all Done work items" link or
  the space's list view, and in a permanent world that list never resets.

No column limits. All five types share these columns.

A card that will not happen is moved to Done with the label `wont-do`, not
given its own column. Team-managed spaces do not let anyone set a resolution
by hand, and the automation that could set one draws on a small monthly
allowance on the Free plan. The label keeps a rejected idea on record, so it
is not proposed again from scratch, and a filter on it separates what was
built from what was dropped.

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
("Nether hub era") are Jira versions on the space. A team-managed space
cannot create versions until its Releases feature is switched on, so setup
enables it. No versions are created at launch; the first is added when the
players name one.

### Starting content

The board starts empty. Cards go in as ideas and work come up.

### Link to engineering

Software work stays in `JDWLABS` on the main site. When an Idea or Problem
needs code, a `JDWLABS` ticket is filed and its URL is pasted on the FWB
card. The FWB card stays open until the players see the result in-game.

## Setup

Recorded as it was done, because two steps did not go as designed.

1. **Site.** `fwb-minecraft.atlassian.net`, created by the owner in their
   own browser. Atlassian's signup scores the browser with a bot check and
   rejects an automated one, so this step cannot be scripted. A new site
   starts on a Premium trial, not on Free.
2. **Space.** Signup creates a first space unasked, named "My Kanban Space"
   with key `KAN`. It was empty and team-managed, so it was renamed to
   "FWB Minecraft" / `FWB` in place. Its board keeps the label "KAN board",
   which shows only in the browser tab title. Atlassian's sample space was
   deleted.
3. **Configuration.** What the REST API could and could not do in a
   team-managed space:

   | Change                               | REST API | Done through      |
   |--------------------------------------|----------|-------------------|
   | Rename the space and its key         | Yes      | API               |
   | Rename an existing status            | Yes      | API               |
   | Enable the Releases feature          | Yes      | API               |
   | Rename a work type                   | No       | Space settings    |
   | Create a field, add it to a type     | No       | Space settings    |
   | Rename, add or reorder board columns | No       | Board, Configure columns |

   The API refuses to rename a work type because the type belongs to the
   space, not the site. A board column has its own name, separate from the
   status behind it, so renaming a status does not rename its column.
   Adding a column through the board creates its status in every work type's
   workflow, which is what keeps the five types on the same five statuses.
4. **Players.** Inviting someone to the site is not enough. A new site
   member could not see or create cards in the space until they were also
   added to its Member role, so each invite is followed by that. Members
   can create and move cards; only the owner administers the space.

## Verification

Checked on 2026-10-06 by creating one card of each type with only a summary,
moving it through every column and deleting it: the first six items below
hold, except the two about the done list, which were not exercised. Those
test cards used the numbers 1 to 5, so the first real card is `FWB-6`. The
last two items wait on the invites. Also outstanding: confirm the site drops
to the Free plan when the Premium trial ends.

- The space has the five work types, five columns in order, and the Category
  and Location fields with the listed values.
- A card of each type can be created with only a summary.
- The Releases feature is on: the option to create a version is offered.
- A card labelled `wont-do` and moved to Done is returned by a filter on
  that label in the space's list view, which is where it will still be found
  once the board has cleared it.
- The full list of done cards opens from the Done column.
- A card of each type can be moved through all five columns.
- Each of the three players can create a card and move it between columns.
- Signed in as one of the two casual players, the main site's `JDWLABS` and
  `CAREER` spaces are not reachable. They hold no account on that site, so
  this confirms nothing was shared by mistake.

## Out of scope

Each of these is a separate piece of work with its own design.

- **In-game `!idea` command** that files an Idea card through the server
  agent. First follow-up after the board has had two weeks of use; filed as a
  `JDWLABS` ticket then. It uses the dedicated account described above, and
  that account's reach is checked against the main site before its token is
  issued. Open questions it carries: how the agent holds the credential,
  spam and rate limits, and which player a card is attributed to.
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
