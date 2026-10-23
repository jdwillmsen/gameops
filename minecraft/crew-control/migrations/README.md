# Migrations owned by crew-control, applied from jdwlabs/platform

crew-control does not run migrations. The `minecraft` schema is applied by the
Flyway job in `jdwlabs/platform`
(`tenants/jdwillmsen/services/jdwillmsen-schemas/postInstall/`), which reads
every migration from the ConfigMap `jdwillmsen-migrations` in
`migrations-configmap.yaml`. The files here are the reviewed source; the
platform repo is where they ship.

| File | Goes to |
| --- | --- |
| `V11__minecraft_crew_events.sql` | The plain SQL: what the DB tests apply to a throwaway database. |
| `V11__minecraft_crew_events.configmap.yaml` | The same SQL as the entry to paste: append it to the end of `data:` in `migrations-configmap.yaml` (after `V10__minecraft_crew_jobs.sql`). |

A platform PR that adds an entry must keep it byte-identical to the plain file
apart from the four-space indent; a test here
(`internal/events/migration_test.go`) fails if the two files here drift.
Flyway versions are global, so check the ConfigMap for a V11 before opening
that PR. No grant is needed: V2's default privileges give `app` the schema.
