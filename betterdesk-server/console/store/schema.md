# The console's database schema

`store.DB.Migrate` creates and maintains the console's tables on SQLite and
PostgreSQL. It does what the Node console's start code did
(`web-nodejs/services/dbAdapter.js`, `init()`), statement for statement and in
the same order, so a database made by Go is the database made by Node at
version 3.5.264, and an older database is upgraded the same way.

This file lists every step, says how the two databases and the three ways a
database comes to exist differ, and documents the value types that make a row
look in JSON as it did from Node.

## What Migrate does

- It creates the 55 tables and their indexes when they are missing, from the
  files in `schema/sqlite` and `schema/postgres` (one file per table).
  `panel_sessions` is not among them: the session package creates it.
  `agent_templates` is among them since upstream 3.5.264 created it at start
  (before that only the first request of a template route made it, BUGS D11):
  on SQLite without its two binding columns when an older table is there, as
  Node left it; on PostgreSQL with them.
- It adds the columns later versions added, in the order and with the
  definition Node used.
- It runs the data fixes that came with those columns: token hashes, bundle
  slugs, bundle product types, and the two default groups. It keeps every
  access token as its hash only (BUGS RV128).
- It only adds, with two exceptions on PostgreSQL. No column or table is
  dropped or renamed, and no table is rebuilt. `agent_bundles.product_type`
  gets `SET NOT NULL` and `SET DEFAULT`, see below, and
  `alert_rules.condition_value` is widened from Node's four-byte `real` to
  `double precision` (BUGS AE4), the one change of a column's type: a fresh
  database has the wider type from its file, an older one gets it from the
  step `widen alert_rules.condition_value`.
- A table that exists is left as it is, also when the signal server created it
  first with other column types. `Shape` tells which variant is there.
- Every step checks what is there before it changes anything, so a second run
  does nothing and logs nothing.

Each step runs in a transaction. Steps that only run a schema file share one
transaction with the file steps next to them, so a fresh database needs about
twenty commits instead of one per table; every other step has its own. On
SQLite a transaction takes the write lock at once, so a second process waits
(up to the five second busy timeout) and then finds the work done. On
PostgreSQL Migrate holds the advisory lock
`(1651794787, hashtext(current_schema()))` for the whole run, so a second
process waits until the first is finished.

Migrate logs through the `logging` package with the component `DB`:

    [DB] Migration: created 55 tables: peer, device_inventory, ...
    [DB] Migration: added users.first_name
    [DB] Migration: added the default row to user_groups
    [DB] Migration: hashed 3 access tokens
    [DB] Migration: gave 2 agent bundles a slug
    [DB] Migration: set product_type of 2 agent bundles
    [DB] WARN: Migration agent_bundles.slug error: ...

A step marked "tolerated" below logs a warning when it fails and the migration
goes on, as Node caught the error there. Any other failing step stops the
migration with an error that names the step.

## The steps

"SQLite" and "PostgreSQL" give the position of the step in that database's
order (the two orders differ, as they did in Node). "Lines" are those of
`web-nodejs/services/dbAdapter.js` at commit 948fb226.

### Tables

Step `create <table>` runs the table's file: `CREATE TABLE IF NOT EXISTS` and
the `CREATE INDEX IF NOT EXISTS` statements Node ran right after it. Condition:
none, the statements do nothing when the table or index exists.

Order on SQLite: peer, device_inventory, device_telemetry, activity_sessions,
activity_summaries, tickets, ticket_comments, ticket_attachments, alert_rules,
alert_history, remote_commands, network_targets, network_checks, dlp_policies,
dlp_events, saved_reports, studio_flows, tenants, tenant_devices,
tenant_users, pending_registrations, agent_bundles, agent_templates,
agent_bundle_builds, then users, audit_log, access_tokens, login_attempts,
account_lockouts, folders, address_books, settings, branding_config, branding_profiles, relay_sessions,
device_folder_assignments, peer_sysinfo, peer_metrics, audit_connections,
audit_files, audit_alarms, user_groups, panel_access_policies,
client_feature_policies, user_group_members, device_groups,
device_group_members, device_group_user_access,
device_group_user_group_access, strategies, strategy_assignments,
user_peer_grants, notification_reads, email_notification_log, and
user_avatars after the `users` columns.

Order on PostgreSQL: peer, users, audit_log, access_tokens, login_attempts,
account_lockouts, folders, address_books, notification_reads,
email_notification_log, settings, branding_config, branding_profiles,
relay_sessions, device_inventory, device_telemetry, tickets, ticket_comments,
ticket_attachments, activity_sessions, activity_summaries, alert_rules,
alert_history, remote_commands, network_targets, network_checks,
dlp_policies, dlp_events, saved_reports, studio_flows, tenants,
tenant_devices, tenant_users, pending_registrations, agent_templates (then its
columns enrolled_device_id and enrolled_at and its index), agent_bundles,
agent_bundle_builds, device_folder_assignments, peer_sysinfo, peer_metrics,
audit_connections, audit_files, audit_alarms, user_groups,
panel_access_policies, client_feature_policies, user_group_members,
device_groups, device_group_members, device_group_user_access,
device_group_user_group_access, strategies, strategy_assignments,
user_peer_grants, user_avatars.

| Table | SQLite lines | PostgreSQL lines |
|---|---|---|
| access_tokens | 369 | 4167 |
| account_lockouts | 390 | 4203 |
| activity_sessions | 765 | 4364 |
| activity_summaries | 776 | 4378 |
| address_books | 405 | 4224 |
| agent_bundle_builds | 1103 | 4703 |
| agent_bundles | 1083 | 4621 |
| agent_templates | 1065 of 3.5.264 | 4617 of 3.5.264 (columns and index 4630-4632) |
| alert_history | 850 | 4411 |
| alert_rules | 834 | 4392 |
| audit_alarms | 505 | 4812 |
| audit_connections | 476 | 4777 |
| audit_files | 490 | 4794 |
| audit_log | 359 | 4154 |
| branding_config | 419 | 4264 |
| branding_profiles | 424 (again at 697, which never does anything) | 4272 |
| client_feature_policies | 534 | 4847 |
| device_folder_assignments | 443 | 4736 |
| device_group_members | 562 | 4881 |
| device_group_user_access | 569 | 4890 |
| device_group_user_group_access | 577 | 4899 |
| device_groups | 552 | 4868 |
| device_inventory | 740 | 4297 |
| device_telemetry | 749 | 4308 |
| dlp_events | 931 | 4509 |
| dlp_policies | 919 | 4488 |
| email_notification_log | 621 | 4244 |
| folders | 397 | 4213 |
| login_attempts | 383 | 4193 |
| network_checks | 901 | 4461 |
| network_targets | 885 | 4442 |
| notification_reads | 613 | 4234 |
| panel_access_policies | 524 | 4836 |
| peer | 287 | 4118 |
| peer_metrics | 467 | 4765 |
| peer_sysinfo | 449 | 4744 |
| pending_registrations | 995 | 4595 |
| relay_sessions | 433 | 4284 |
| remote_commands | 863 | 4426 |
| saved_reports | 959 | 4528 |
| settings | 414 | 4256 |
| strategies | 585 | 4908 |
| strategy_assignments | 596 | 4921 |
| studio_flows | 977 | 4542 |
| tenant_devices | 1173 | 4572 |
| tenant_users | 1181 | 4581 |
| tenants | 1161 | 4558 |
| ticket_attachments | 818 | 4351 |
| ticket_comments | 809 | 4340 |
| tickets | 793 | 4321 |
| user_avatars | 664 (tolerated) | 5004 |
| user_group_members | 544 | 4859 |
| user_groups | 516 | 4826 |
| user_peer_grants | 605 | 4932 |
| users | 346 | 4138 |

On SQLite the files hold the statement text byte for byte as Node ran it,
because SQLite keeps that text in `sqlite_master`. A Go-made SQLite file
therefore has the same `.schema` output as a Node-made one.

On PostgreSQL five groups of indexes are created apart from their tables, at
the place Node created them:

| Step | What | Condition | SQLite | PostgreSQL | Lines |
|---|---|---|---|---|---|
| `run indexes_core` | `idx_peer_online`, `idx_peer_deleted`, `idx_token_lookup`, `idx_token_hash_lookup`, `idx_relay_sessions_expires`, `idx_activity_device`, `idx_activity_time`, `idx_alert_history_rule`, `idx_alert_history_device`, `idx_remote_commands_device`, `idx_network_checks_target`, `idx_network_checks_time` | `IF NOT EXISTS` | with their tables; the first five do not exist on SQLite | after network_checks | 4473-4484 |
| `run indexes_tenants` | `idx_tenant_devices_tenant`, `idx_tenant_devices_device`, `idx_tenant_users_tenant` | `IF NOT EXISTS` | with their tables | after tenant_users | 4589-4591 |
| `run indexes_agent_bundles` | `idx_agent_bundles_bundle_id`, `idx_agent_bundles_hash` | `IF NOT EXISTS` | with the table | after the three agent_bundles columns | 4642-4643 |
| `run indexes_agent_bundle_builds` | `idx_agent_bundle_builds_hash`, `idx_agent_bundle_builds_status` | `IF NOT EXISTS` | with the table | after the five agent_bundle_builds columns | 4731-4732 |
| `run indexes_agent_templates` | `idx_agent_templates_token` | `IF NOT EXISTS` | with the table | after the two agent_templates columns | 4632 of 3.5.264 |

### Added columns

Every step is `ALTER TABLE <table> ADD COLUMN <column> <definition>` (with
`IF NOT EXISTS` on PostgreSQL). Condition for all of them: the table exists
and does not have the column. Existing rows get the column's default.

| Step | Definition on SQLite | Definition on PostgreSQL | SQLite | PostgreSQL | Lines |
|---|---|---|---|---|---|
| `add peer.status_online` | `INTEGER DEFAULT 0` | no step | after peer, tolerated | | 305-321 |
| `add peer.last_online` | `TEXT` | no step | tolerated | | 305-321 |
| `add peer.is_deleted` | `INTEGER DEFAULT 0` | no step | tolerated | | 305-321 |
| `add peer.user` | `TEXT DEFAULT ''` | no step | tolerated | | 305-321 |
| `add peer.is_banned` | `INTEGER DEFAULT 0` | no step | tolerated | | 305-321 |
| `add peer.banned_at` | `TEXT` | no step | tolerated | | 305-321 |
| `add peer.banned_reason` | `TEXT DEFAULT ''` | no step | tolerated | | 305-321 |
| `add peer.folder_id` | `INTEGER DEFAULT NULL` | no step | tolerated | | 305-321 |
| `add peer.tags` | `TEXT DEFAULT ''` | no step: `peer` has no `tags` on PostgreSQL (BUGS.md D4) | tolerated; runs on every fresh file, the column is not in the `CREATE TABLE` | | 314 |
| `add dlp_policies.policy_type` | `TEXT DEFAULT ''` | same | after dlp_events, tolerated | after dlp_policies | 948-953, 4502-4506 |
| `add dlp_policies.action` | `TEXT DEFAULT 'log'` | same | tolerated | | 948-953, 4502-4506 |
| `add dlp_policies.scope` | `TEXT DEFAULT ''` | same | tolerated | | 948-953, 4502-4506 |
| `add agent_templates.enrolled_device_id` | no step: an older SQLite table keeps the columns it has, as Node left it | `TEXT DEFAULT NULL` | | after agent_templates | 4630 of 3.5.264 |
| `add agent_templates.enrolled_at` | no step | `TIMESTAMPTZ DEFAULT NULL` | | | 4631 of 3.5.264 |
| `add agent_bundles.config_fingerprint` | `TEXT NOT NULL DEFAULT ''` | same | after agent_bundle_builds | after agent_bundles | 1128-1139, 4639 |
| `add agent_bundles.client_commit` | `TEXT DEFAULT NULL` | same | | | 1128-1139, 4640 |
| `add agent_bundles.active_generation_hash` | `TEXT DEFAULT NULL` | same | | | 1128-1139, 4641 |
| `add agent_bundle_builds.client_commit` | `TEXT DEFAULT NULL` | same | after the agent_bundles columns | after agent_bundle_builds | 1140-1153, 4726 |
| `add agent_bundle_builds.generation_id` | `TEXT DEFAULT NULL` | same | | | 1140-1153, 4727 |
| `add agent_bundle_builds.config_fingerprint` | `TEXT DEFAULT NULL` | same | | | 1140-1153, 4728 |
| `add agent_bundle_builds.progress_percent` | `INTEGER NOT NULL DEFAULT 0` | same | | | 1140-1153, 4729 |
| `add agent_bundle_builds.progress_phase` | `TEXT DEFAULT ''` | same | | | 1140-1153, 4730 |
| `add users.last_login` | `TEXT` | `TIMESTAMPTZ` | after email_notification_log, tolerated | after the default groups | 633-658, 4956-4958 |
| `add users.auth_provider` | `TEXT DEFAULT 'local'` | same | tolerated | | 633-658, 4959-4961 |
| `add users.preferred_language` | `TEXT DEFAULT NULL` | same | tolerated | | 633-658, 4962-4964 |
| `add users.totp_secret` | `TEXT DEFAULT NULL` | same | tolerated | | 633-658, 4965-4967 |
| `add users.totp_enabled` | `INTEGER DEFAULT 0` | `BOOLEAN DEFAULT FALSE` | tolerated | | 633-658, 4968-4970 |
| `add users.totp_recovery_codes` | `TEXT DEFAULT NULL` | same | tolerated | | 633-658, 4971-4973 |
| `add users.first_name` | `TEXT DEFAULT ''` | same | tolerated; runs on every fresh database | after the device_groups columns; runs on every fresh database | 633-658, 4988-5000 |
| `add users.last_name` | `TEXT DEFAULT ''` | same | tolerated | | 633-658, 4988-5000 |
| `add users.email` | `TEXT DEFAULT ''` | same | tolerated | | 633-658, 4988-5000 |
| `add users.phone` | `TEXT DEFAULT ''` | same | tolerated | | 633-658, 4988-5000 |
| `add users.role_display` | `TEXT DEFAULT ''` | same | tolerated | | 633-658, 4988-5000 |
| `add users.avatar_url` | `TEXT DEFAULT ''` | same | tolerated | | 633-658, 4988-5000 |
| `add users.guid` | `TEXT DEFAULT ''` | same | last of the users columns, tolerated | before `first_name` | 647, 4974-4976 |
| `add settings.updated_at` | `TEXT` without default, rows filled with `datetime('now')`, see below | `TIMESTAMPTZ DEFAULT NOW()` | after user_avatars, tolerated | last step, tolerated | 678-684, 5016-5022 |
| `add branding_config.updated_at` | `TEXT` without default, rows filled with `datetime('now')`, see below | no step | after settings, tolerated | | 687-693 |
| `add device_groups.source_type` | `TEXT DEFAULT 'manual'` | same | after branding_config, tolerated | after the users columns up to `guid` | 709-717, 4978-4985 |
| `add device_groups.tag_filter` | `TEXT DEFAULT ''` | same | tolerated | | 709-717, 4978-4985 |
| `add access_tokens.token_kind` | `TEXT NOT NULL DEFAULT 'account'` | same | last step | last step | not in Node (BUGS RV1) |

`access_tokens.token_kind` is `device` for a token the console issued for a
device and `account` for the token of an account's sign-in; only a device
token proves a device (BUGS RV1). The tokens that are there when the column
comes were all made by sign-ins, and the default makes each an account's
token. So does it for the rows of anything that writes the table without
naming the kind, a rolled-back Node console among them.

`settings.updated_at` and `branding_config.updated_at` on SQLite: SQLite does
not add a column whose default is an expression to a table that has rows
(`Cannot add a column with non-constant default`). On a table from before the
column existed the step adds it as `TEXT` without a default and gives the
rows that are there the time of the migration, so the console's statements
that write `updated_at` work there; a fresh install creates the column with
its default. The Node console ran the statement with the default, logged the
error at every start and went on without the column (BUGS.md D17). Every 3.x
release created both tables with the column, so this concerns 2.x files only.

### Data fixes

| Step | What | Condition | SQLite | PostgreSQL | Lines |
|---|---|---|---|---|---|
| `hash access tokens` | Adds `access_tokens.token_hash TEXT` and the partial unique index `idx_access_tokens_hash`, then sets `token_hash` to the SHA-256 (hex) of `token` | column: missing. Index: `IF NOT EXISTS`. Rows: `token_hash` is NULL or empty and `token` is not empty | last step | right after access_tokens | 324-342, 4181-4190 |
| `agent_bundles.slug` | Adds `agent_bundles.slug TEXT DEFAULT NULL` and the partial unique index `idx_agent_bundles_slug`, then gives each bundle without a slug one: from its name, else from its bundle id, numbered `-2`, `-3` while taken | column: missing. Index: `IF NOT EXISTS`. Rows: `slug` is NULL or empty. Tolerated | after the agent_bundle_builds columns | after `run indexes_agent_bundles` | 1022-1049, 4644-4666 |
| `agent_bundles.product_type` | Adds `agent_bundles.product_type TEXT NOT NULL DEFAULT 'betterdesk-support'`; sets every value that is not `betterdesk-support` to it | column: missing. SQLite: the `UPDATE` runs at every start. PostgreSQL: the `UPDATE` runs only when the column was already there; then also `SET NOT NULL` when the column is nullable and `SET DEFAULT 'betterdesk-support'` when its default is another. Tolerated | after `agent_bundles.slug` | after `agent_bundles.slug` | 1051-1079, 4667-4701 |
| `seed user_groups` | Inserts the group `Default` (note `Default user group`, random guid) | the table has no row | after the device_groups columns | after user_peer_grants | 720-726, 4942-4946 |
| `seed device_groups` | Inserts the group `Default` (note `Default device group`, random guid) | the table has no row | after `seed user_groups` | after `seed user_groups` | 727-733, 4947-4951 |
| `widen alert_rules.condition_value` | Changes the column to `double precision`, then sets every limit to the shortest number that names its four bytes, the value Node and the Go console read (0.1, where the cast alone gives 0.10000000149011612); NaN and the infinities stay. Logs `Migration: widened alert_rules.condition_value to double precision`. Not in Node (BUGS AE4); a Node console started on the wider column reads the same limits | the column is `real` | no step | last step | |
| `keep access tokens as their hash` | Every row whose `token` holds the token itself gets `token_hash` = the SHA-256 of that token and `token` = `sha256 ` and that hash, so a copy of the database holds no token. Logs `Migration: kept N access tokens as their hash only`. Not in Node (BUGS RV128): the console writes its rows that way, and a Node console after a rollback still finds them by the hash | `token` is not empty and does not start with `sha256 `; runs at every start | last step | last step | |

The slug rules are those of `web-nodejs/services/agentBundleService.js`
(`slugifyName`, `allocateUniqueSlug`); `SlugifyBundleName` and
`AllocateBundleSlug` are the Go versions, tested against Node's output.

## The one statement that changes a column

On PostgreSQL the step `agent_bundles.product_type` can run
`ALTER COLUMN product_type SET NOT NULL` and `SET DEFAULT 'betterdesk-support'`.
Node did the same (4692-4697). It applies to installs that last ran a console
between 3.5.16 and 3.5.86, which created the column with the default
`'support-agent'`. The type does not change and no row is lost. After a
rollback to such a version its own start code sets the default back to its
value and rewrites the rows to `support-agent`; that is what happens between
two Node versions as well.

On SQLite a default cannot be changed. A file that got the column from a
console between 3.3.21 and 3.5.86 keeps that version's default (`'agent'`,
from 3.5.16 on `'support-agent'`); only the rows are rewritten, at every
start. This is the one place where an upgraded 3.x database differs from a
fresh one (checked with the schema of 3.4.0).

## Which installs upgrade directly

Every 3.x release. Checked with the schemas the releases 3.0.0, 3.4.0 and
3.5.200 create with their own start code (`testdata/old`): after `Migrate`
they have the tables, columns, constraints and indexes of a fresh install, in
another column order, with the SQLite exception above, and every row is still
there. With the build tag `nodeschema` the same upgrade is done by the current
Node console and the two results are compared.

Steps that only matter for installs older than 3.0.0 are kept because Node
kept them: the `peer` columns, `users.last_login` to `totp_recovery_codes`,
`settings.updated_at`, `branding_config.updated_at`, the `dlp_policies`
columns, `access_tokens.token_hash`, `agent_bundles.slug`. Whether a 2.x
database upgrades completely was not checked.

Not handled here: an install that still has a separate `auth.db` (DECISIONS
D1). `Migrate` works on the one database it is given; the import of `auth.db`
is its own task.

## Tables both programs create

The signal server (`betterdesk-server/db`) creates 10 of these tables on
SQLite and 17 on PostgreSQL itself, with other column types. Whoever starts
first decides the shape of each, and nothing converts it (DECISIONS D2).
`Migrate` does on such a table what Node did: `CREATE TABLE IF NOT EXISTS`
does nothing, the column steps add what is missing, the indexes are created
when no index of that name exists.

What `Migrate` adds to a database the signal server set up first:

- `users`: `preferred_language`, `first_name`, `last_name`, `email`, `phone`,
  `role_display`, `avatar_url`. Everything else is already there in Go's
  form: `id BIGINT` on PostgreSQL, `totp_secret NOT NULL DEFAULT ''` on
  PostgreSQL, `role NOT NULL DEFAULT 'viewer'`, `is_server_admin`.
- `device_groups`: nothing (Go's table has `source_type` and `tag_filter`).
- Indexes of the same tables that Go names differently, for example
  `idx_peer_metrics_peer_time` next to Go's `idx_peer_metrics_peer_created`.
- The 44 tables only the console has (37 on PostgreSQL, where Go also
  creates folders, the group member and access tables and `peer_sysinfo`).

Checked in both start orders on both databases: the catalog (tables, columns
with type, nullability and default, keys, constraints, indexes, sequences,
triggers, on PostgreSQL functions, on SQLite the stored statement text)
equals the one in the dumps taken from a real Node console and signal server
(`testdata/dumps/shared-*.sql`), with no difference besides `panel_sessions`.
With the build tag `nodeschema` the comparison runs against the real Node
console.

The signal server gives `users` the column `credentials_changed_at` and the
trigger `users_credentials_changed` that stamps it when the password or the
second factor changes (RV181): in its `CREATE TABLE` when it starts first, by
`ALTER TABLE` when the console made the table. The dumps carry both in the
text the signal server creates, added by hand on 2026-11-21 since the Node
console no longer runs. `TestCredentialsChangedAtInEveryOrigin` checks the
column and the trigger on every origin after both migrations.

Known limits of the two shapes, not changed here (BUGS.md D5): the signal
server cannot use an `address_books` table in the console's SQLite shape;
`audit_files.is_file` is `INTEGER` in one shape and `BOOLEAN` in the other on
PostgreSQL; `users.totp_secret` is `NOT NULL` in Go's PostgreSQL shape.

## Where Migrate differs from Node's init()

- PostgreSQL: Node looked for existing columns in every schema of the
  database (`information_schema.columns` without a schema filter). Migrate
  looks in the current schema only. On an install with one schema this is the
  same.
- A step that fails is rolled back as a whole, and so are the table files
  that share its transaction. Node ran its statements one by one, so the ones
  before the failing one stayed.
- Node swallowed some errors without a word (the `peer`, `users` and
  `dlp_policies` columns on SQLite, `settings.updated_at` on PostgreSQL).
  Migrate logs a warning for them.
- Node logged only some added columns. Migrate logs every change.
- `access_tokens.token_kind` is not in Node (BUGS RV1).
- Access tokens are kept as their hash only: Migrate rewrites `token` of
  every row that holds the token itself (BUGS RV128).
- SQLite: the second `CREATE TABLE IF NOT EXISTS branding_profiles` (697) is
  left out. It could never do anything.

## Shape

`db.Shape()` answers what the tables look like, read once at the end of
`Migrate` (or by `db.Inspect(ctx)` after something else changed the schema):

    shape := db.Shape()
    shape.Table("address_books").Has("username")              // keyed by name or by user id
    shape.Table("audit_files").Type("is_file") == store.TypeBoolean
    shape.Table("users").Type("id") == store.TypeBigInt       // ids go out as strings
    shape.Table("users").Column("totp_secret").NotNull

| Method | Answer |
|---|---|
| `Shape.Table(name)` | the table; for a missing table `Exists()` is false and every column is `TypeMissing` |
| `Shape.Tables()` | all table names, sorted |
| `Table.Has(column)` | whether the column exists |
| `Table.Type(column)` | `TypeInteger`, `TypeBigInt`, `TypeBoolean`, `TypeText`, `TypeReal`, `TypeDouble`, `TypeNumeric`, `TypeBlob`, `TypeTimestampTZ`, `TypeTimestamp`, `TypeDate`, `TypeDateTime` (SQLite text declared `DATETIME`), `TypeJSON`, `TypeJSONB`, `TypeOther`, `TypeMissing` |
| `Table.Column(column)` | name, declared type, `Type`, `NotNull`, `Default`, `PrimaryKey`, `Serial` (the database assigns the value: `INTEGER PRIMARY KEY` on SQLite, `SERIAL`, `BIGSERIAL` or identity on PostgreSQL) |
| `Table.Columns()` | the columns in table order |

`Shape()` panics when neither `Migrate` nor `Inspect` has run. The shape of
each `DB` is kept in a package-level map keyed by a weak pointer to the `DB`,
because `db.go` was not to be changed; a field in `DB` would do the same
without the map.

The variants that matter (checked by `TestShapeOfEveryVariant` and
`TestShapeOfSharedDumps`):

| Column | SQLite | PostgreSQL, console first | PostgreSQL, signal server first |
|---|---|---|---|
| `users.id`, `folders.id`, `device_groups.id` and the columns that point at them | `INTEGER` | `integer` (`SERIAL`) | `bigint` (`BIGSERIAL`) |
| `audit_files.is_file` | `INTEGER` | `boolean` | `integer` |
| `audit_files.files_json`, `audit_alarms.details`, `strategies.permissions`, the five JSON columns of `peer_sysinfo` | `TEXT` | `jsonb` | `text` |
| `users.totp_secret` | nullable | nullable | `NOT NULL DEFAULT ''` |
| `address_books` | `user_id` when the console made it, `username` when the signal server did | `username` | `username` |

## Value types

The Node console sent rows to the browser as its drivers returned them, so the
JSON of a column follows the column's type. The types in `values.go` scan a
column and marshal it the way Node sent it (DECISIONS D2 and D3).

| Type | For | JSON |
|---|---|---|
| `Time` | time columns | SQLite: the stored text. PostgreSQL: ISO in UTC with milliseconds, `"2026-11-10T01:05:51.694Z"`. `null` for NULL |
| `Bool` | flags | `0` or `1` from an integer column, `true` or `false` from a `boolean` column, `null` for NULL |
| `Int64String` | ids, counts, sizes | a number; a string for PostgreSQL `bigint` and `numeric` (`"42"`); `null` for NULL |
| `Float` | `REAL`, `double precision` | a number; for a PostgreSQL `real` the short form of the four-byte value (`0.1`); `null` for NULL, NaN and infinity |
| `JSONText` | JSON columns the console passed through | text column: the text as a JSON string. `json` or `jsonb` column: the value |
| `JSONParsed` | JSON columns the console parsed (`safeJsonParse(value, fallback)`) | the value on both databases; `Fallback` (`"[]"`, `"{}"`) when the text does not parse; `null` for NULL |
| `NullString` | nullable text | the text or `null`; `OrEmpty()` and `OrNull()` for the places where the console wrote "the value or else an empty string" or "the value or else null" |

A value has to know its column's type: a PostgreSQL `integer` and a `bigint`
both reach Go as `int64`, and so on. Read rows that hold these types with
`store.Select` or `store.SelectRow` instead of `db.Query` and `db.QueryRow`;
they take a `Querier`, so they work on a `DB` and in a transaction:

    rows, err := store.Select(ctx, db, `SELECT id, name, created_at FROM folders ORDER BY sort_order, name`)
    ...
    for rows.Next() {
        var f struct {
            ID        store.Int64String `json:"id"`
            Name      string            `json:"name"`
            CreatedAt store.Time        `json:"created_at"`
        }
        if err := rows.Scan(&f.ID, &f.Name, &f.CreatedAt); err != nil { ... }
    }

    var total store.Int64String
    err := store.SelectRow(ctx, db, `SELECT COUNT(*) FROM tickets`).Scan(&total)   // 5 on SQLite, "5" on PostgreSQL

`Time`, `Int64String`, `Float` and `JSONText` return an error when a value
that needs the column type is scanned without it. `Bool`, `NullString` and
`JSONParsed` work with a plain scan too.

For values that do not come from a scan:
`store.NewInt64String(id, shape.Table("users").Type("id"))`.

The values write strings the way `JSON.stringify` does. The encoder that
sends them must not escape HTML (`SetEscapeHTML(false)`), or `<`, `>` and `&`
inside JSON columns come out as `<` and so on.

Writing:

| Helper | Use |
|---|---|
| `NowSQL(dialect)` | the SQL for "now" Node's statements used: `datetime('now')` or `NOW()`. Put it into the statement; do not bind a Go time |
| `SQLiteTime(t)` | text as `datetime('now')` writes it, `2026-11-10 01:05:50` |
| `ISOTime(t)` | text as `Date.toISOString()` writes it, `2026-11-10T01:05:51.694Z` (studio flows, alert history, inventory) |
| `DateTimeMillis(t)` | the expiry form Node built for `access_tokens.expires_at` and `account_lockouts.locked_until`, `2026-11-17 01:05:51.694` |
| `BoolArg(columnType, value)` | `true`/`false` for a `boolean` column, `1`/`0` for an integer one |
| `ParseTime(text)` | the instant behind any of the text forms; `Time.Instant()` does it for a scanned value |

### What the drivers return

Measured with `testdata/nodeschema/vectors.js` (pg 8.23.1 with pg-types 2.2.0,
better-sqlite3 13.0.3) and `TestDriverValueTypes` (pgx 5.10 through
`database/sql`, modernc.org/sqlite 1.55). The JSON column is
`JSON.stringify` of what Node's driver returned; the files in
`testdata/vectors` have every case.

SQLite:

| Declared type | better-sqlite3 gives | JSON from Node | Go driver gives | Read with |
|---|---|---|---|---|
| `TEXT` | string | `"2026-11-10 01:05:50"` | `string` | `Time`, `NullString`, `JSONText`, `JSONParsed`, `string` |
| `INTEGER` | number | `1`, `5000000000` | `int64` | `Int64String`, `Bool`, `int64` |
| `REAL` | number | `0.1`, `2` | `float64` | `Float`, `float64` |
| `BLOB` | Buffer | `{"type":"Buffer","data":[...]}` | `[]byte` | `[]byte` |
| `DATETIME` | string | `"2026-11-10 01:05:50"` | `time.Time`: the driver parses the text | `Time` |
| expression (`COUNT(*)`, `MAX(x)`) | number or string | `44` | the value's own type, no declared type | as above |

The `DATETIME` row is a trap. Ten columns are declared that way
(`dlp_policies.created_at` and `updated_at`, `dlp_events.created_at`,
`saved_reports.created_at`, `pending_registrations.created_at` and
`updated_at`, `tenants.created_at` and `updated_at`,
`tenant_devices.assigned_at`, `tenant_users.assigned_at`). modernc parses
their text into a `time.Time`, where better-sqlite3 returned the text. `Time`
formats it back the way `datetime('now')` writes it, which is the only form
Node's statements put there. Text in another form in such a column comes out
changed (`2026-11-10T01:05:50.930Z` as `2026-11-10 01:05:50.93`); select
`CAST(column AS TEXT)` to get the stored text.

PostgreSQL:

| Column type | node-postgres gives | JSON from Node | Go driver gives (type name) | Read with |
|---|---|---|---|---|
| `text` | string | `"plain"` | `string` (`TEXT`) | `NullString`, `JSONText`, `JSONParsed`, `Time`, `string` |
| `integer`, `smallint`, `serial` | number | `42` | `int64` (`INT4`, `INT2`) | `Int64String`, `Bool`, `int64` |
| `bigint`, `bigserial` | string | `"42"`, `"5000000000"` | `int64` (`INT8`) | `Int64String` |
| `COUNT(*)`, `SUM(integer)` | string | `"75"` | `int64` (`INT8`) | `Int64String` |
| `COUNT(*)::INTEGER` | number | `75` | `int64` (`INT4`) | `Int64String` |
| `numeric`, `SUM(bigint)`, `AVG(integer)` | string | `"12.50"`, `"17.5000000000000000"` | `string` (`NUMERIC`) | `Int64String` (keeps the text) |
| `boolean` | boolean | `true` | `bool` (`BOOL`) | `Bool` |
| `real` | number, parsed from PostgreSQL's short text | `0.1`, `99.99` | `float64` holding the four-byte value: `0.10000000149011612` (`FLOAT4`) | `Float` |
| `double precision` | number | `0.1` | `float64` (`FLOAT8`) | `Float`, `float64` |
| `real` NaN, Infinity | NaN, Infinity | `null` | `float64` | `Float` |
| `timestamptz` | Date | `"2026-11-10T01:05:51.694Z"`: microseconds cut, not rounded | `time.Time` (`TIMESTAMPTZ`) | `Time` |
| `timestamptz` infinity | Infinity | `null` | `string` `"infinity"` | `Time` |
| `timestamp` (no zone) | Date, read as local time of the process | with `TZ=Europe/Berlin`: `2026-11-10 01:05:51.694` gives `"2026-11-10T00:05:51.694Z"` | `time.Time` in UTC with the stored clock reading (`TIMESTAMP`) | `Time` |
| `date` | Date at local midnight | with `TZ=Europe/Berlin`: `"2026-11-09T23:00:00.000Z"` | `time.Time` (`DATE`) | `Time` |
| `jsonb` | the parsed value | `{"2":{"x":null},"10":[1,100],"a":1}`: numbers as doubles, keys that are array indexes first | `[]byte` with PostgreSQL's text (`JSONB`) | `JSONText`, `JSONParsed` |
| `json` | the parsed value | `{"b":2,"a":1}` for the stored `{"b": 1,  "a": 1.0, "b": 2}` | `[]byte` (`JSON`) | `JSONText`, `JSONParsed` |
| `bytea` | Buffer | `{"type":"Buffer","data":[...]}` | `[]byte` (`BYTEA`) | `[]byte` |

None of the console's own columns is `timestamp` without zone or `date`; the
rule is there for columns of other tables a query may touch.

`safeJsonParse(null, fallback)` is `null`, not the fallback: `JSON.parse(null)`
does not throw. `JSONParsed` does the same. On a `jsonb` column a stored JSON
string is parsed a second time by `safeJsonParse` (`"5"` becomes `5`, `"text"`
becomes the fallback); `JSONParsed` does that too when it knows the column
type.

Not covered: `LocalWallClock` follows Go's rules for a clock reading that
does not exist or exists twice on the day the clocks change, which may differ
from JavaScript's by the hour that is skipped or repeated.

## Tests

    tools/gotest.sh "./console/store/... -count=1 -race"
    tools/gotest.sh "-tags nodeschema ./console/store/ -run TestNode -count=1"
    DEV_DOCKER_ARGS="-e TZ=Europe/Berlin" tools/gotest.sh "./console/store/ -run 'TestValues|TestSelect|TestDriver' -count=1"

| File | What it checks |
|---|---|
| `schema_test.go` | a fresh database equals the Node dump; a Node database is left alone; a second run changes nothing |
| `shared_test.go` | both start orders with the signal server equal the shared dumps; both sides read and write users, device groups, strategies, address books and audit rows; every origin has `users.credentials_changed_at` and its trigger after both migrations |
| `old_test.go` | the schemas of 3.0.0, 3.4.0 and 3.5.200 with rows upgrade to the current one |
| `steps_test.go` | every column step, the token hashes, slugs, product types, default groups, two migrations at once, the PostgreSQL lock, the log lines, failing steps, this file's step tables |
| `shape_test.go` | the shape of every variant: console first, signal server first, Node first, the four shared dumps, the signal server alone; what the Go drivers return |
| `values_test.go` | the value types against Node's driver output |
| `nodeschema_test.go` (tag `nodeschema`) | the same comparisons against the running Node console, and that Node still starts and reads the same rows after `Migrate` |

`storetest` gives other packages these databases: `Migrated(t)`,
`EachMigrated(t, fn)`, and `EachOrigin(t, fn)` for code that touches a shared
table (console first, signal server first, Node first, on both databases).
`GoServer(t, db)` returns the signal server's own handle on the same
database.
