# M4 Restore: design

Date: 2026-10-16 · Milestone: M4 Restore ([Roadmap](../../BLUEPRINT.md#roadmap)) · Owner: @jufi

The owner asked to skip the design questions: this spec follows the blueprint (Backup engine →
Restore, Security model → Restore row, LAN API `GET /backups/{id}`) and marks every point the
blueprint leaves open as **my call**. Each call becomes a `docs/decisions.md` row (D-056 on) in
the milestone.

## Intent

Backups are worth only what a restore gets back. An office operator with no database knowledge
must be able to say "give me yesterday's data" and get it, without risking the live database by
accident, and must be able to put the live database back when it is damaged, with a safety copy
taken first.

**Exit criterion** (Roadmap): a restored database's `CHECKSUM TABLE` equals the original's for
every table.

## Goals

1. `sevjala restore <backup|latest> [--db NAME] [--into NAME]` restores a backup into a **new**
   database on the controller's MySQL/MariaDB. Production is never touched by default.
2. `sevjala restore <backup|latest> [--db NAME] --replace-production` replaces the live database
   after a fresh pre-restore backup and after the operator types the database name.
3. The backup is taken from the controller's staging when it is there, otherwise fetched from a
   backup PC over the existing mTLS LAN API (`GET /backups/{id}`, new on nodes).
4. The file's sha256 is checked before anything is loaded. A damaged copy is marked `corrupt`
   and the next copy is tried.
5. `sevjala restore --file <path.sql.gz>` loads a file exported earlier (`backups export`), checked
   against the `.json` manifest next to it. **My call**, beyond the blueprint: when the controller
   PC itself dies, a new controller cannot fetch from the old one's nodes (a node accepts one
   controller), so recovery is "export on a backup PC, copy the file to the new server, restore
   from the file". This is the disaster case backups exist for.
6. Progress and the result are shown like `backup now`: steps, a percentage while loading, the
   time taken; Ctrl-C stops watching, the restore carries on in the agent.
7. Start, source copy, target, duration and result are logged with `event` fields.

## Non-goals

- Restore on a node PC. Nodes have no MySQL credentials; there, `backups export` copies the file
  out and the operator loads it where they want.
- Restoring single tables, point-in-time recovery (binlogs, Phase 3), restoring to another
  MySQL server than the configured one, cloning (the owner's next topic, discussed separately).
- An atomic swap of the live database. MySQL cannot rename a database; the blueprint's "drop and
  reload" stands, protected by the pre-restore backup.
- UI or tray (M5–M7).

## Anti-goals (a failure even if it "works")

- Production touched without `--replace-production` **and** the typed name.
- A damaged file loaded (even partly) before its hash is checked.
- `--replace-production` dropping the database before the pre-restore backup is safely in staging.
- A scheduled backup dumping a half-reloaded database and calling it a good backup.
- The DB password on a command line, in a log, or in an error message (D-051 rules apply).

## How it works

### Choosing the backup

The controller resolves the argument (the CLI only passes it on):

- an id `BK-…` → that backup from the catalog;
- `latest` → the newest backup of `--db` (or of the only configured database; with several, a
  usage error lists them) that has a usable source: in staging or at least one `verified` copy.
  Expired and failed backups are skipped.

### Choosing the source (my call: order and fallback)

1. Staging, when `staging/<id>.sql.gz` and its manifest are there.
2. Otherwise each node holding a `verified` copy and currently `online`, newest `verified_at`
   first. Offline nodes are not tried.
3. If a source fails (unreachable, or hash mismatch), the next one is tried; all failing is the
   restore's error, naming each source and why.

A node streams through `GET /backups/{id}` (mTLS, paired controller only), using
`inventory.Store.Open`, so a retention `DELETE` of that backup waits (ErrBusy, retried) until the
stream ends. The controller writes it to `staging/<id>.restore.part` while hashing; the file is
deleted when the restore ends, whatever the result.

### Verifying (blueprint step 2)

- Node copy: size and sha256 checked while it downloads; mismatch → the copy is marked `corrupt`
  in the catalog with the reason (`MarkCopy`), logged, next source. Retention then replaces it
  as it already does for corrupt copies.
- Staging copy: hashed in a first read before loading (a restore reads it twice; AD-11's
  single pass is about the backup path). Mismatch → **my call**: the staging files are deleted
  and the backup unstaged, so backfill never sends a damaged file; logged; next source.
- `--file`: the manifest next to the file (`<name>.json`, as `backups export` writes it) gives
  size and sha256; mismatch or a missing manifest stops the restore. Nothing is marked.

### Loading into a new database (default)

- Target name: `--into NAME`, or **my call** a default of `<database>_restored_<yyyymmdd_hhmm>`
  (the backup's creation time), shown before it starts. Names are checked: 1–64 characters of
  letters, digits, `_` and `$`, so they can be backtick-quoted safely.
- The target must not exist; if it does, the restore refuses and says to pick another name.
- `CREATE DATABASE` with the source database's default character set and collation when that
  database exists on the server (read from `information_schema.SCHEMATA`), else the server's.
- Load: `gunzip` stream → `mysql --default-character-set=utf8mb4 <target>`, password via
  `MYSQL_PWD` exactly as `dump` does; errors through `dump`'s `explain` (plain messages).
- If loading fails, **my call**: the half-made target database is dropped (Sevjala created it a
  moment ago) and the error says so.
- Done: the result names the database and its table count.

### Replacing production (`--replace-production`)

1. CLI: shows what will happen (database, backup, date) and asks the operator to type the
   database name; anything else cancels. The local API also requires `confirm` to equal the
   database name, so no client can skip it. Without a terminal the CLI reads the answer from
   stdin, which only someone scripting it on purpose can give.
2. Refused while a backup of that database is running (409, try again after it).
3. Source chosen and verified **before** anything else changes.
4. Pre-restore backup: a normal backup of the live database, **my call** an ordinary `BK-` backup
   tagged `manual`, not the blueprint's `PRE-RESTORE-<ts>` file. A non-`BK-` file in staging is
   deleted as an orphan by reconcile and never reaches a node; an ordinary backup is distributed,
   listed, kept by `manual`'s keep count, and restorable with this same command. The restore waits
   until it is `created` (complete in staging); if it fails, the restore stops and production is
   untouched.
5. `DROP DATABASE`, `CREATE DATABASE` with the character set and collation it had, load.
6. If loading fails: production is partly loaded. The error says so and gives the exact command
   to put it back: `sevjala restore <pre-restore id> --replace-production`.
7. While steps 4–6 run, scheduled and manual backups of that database are skipped with a logged
   reason, so no backup captures a half-loaded database.

### Size and time

Measured on the owner's controller (2026-10-14): a 45 MB database dumps in about 1 s to a
10.8 MiB file. The owner expects about 500 MB of data: backups stay in the minutes range, but
loading rebuilds every index and is the slow direction, likely several minutes. So:

- the watcher shows a percentage of the compressed file loaded, and the elapsed time;
- the `--replace-production` confirmation states the backup's size and that the database is
  unavailable from the drop until loading ends;
- the M4 smoke records real times for the owner's database (dump, fetch, load) in
  `docs/smoke-tests.md`, so later sizing advice rests on numbers.

### One restore at a time (my call)

The controller runs one restore at a time; a second request gets 409 naming the running one.
Restores run on the agent's lifetime context, so closing the CLI window does not stop them;
stopping the service does (a new-database restore then leaves a partial target the operator can
drop; the log says so).

### Interfaces

**LAN API (node)**: `GET /backups/{id}` (mTLS, paired only) → 200 with the `.sql.gz` body and
`X-Sevjala-Sha256`, `X-Sevjala-Size`, `X-Sevjala-Manifest` (base64 JSON), as `PUT` sends them;
404 not held; 409 busy (being received or deleted). `lanapi.Client.GetBackup` returns the
manifest and the body.

**Local API (controller)**:

- `POST /restore` `{"backup": "<id>|latest", "db": "", "into": "", "replace": false,
  "confirm": "", "file": ""}` → `Restore` (state of the started restore); 400 bad input, 404
  unknown backup, 409 running restore / running backup / target exists / confirm mismatch.
- `GET /restore` → the current or last `Restore`; 404 when none since the agent started.
- `Restore`: `{"id", "backup_id", "database", "target", "replace", "source", "state",
  "step", "bytes", "size", "pre_restore_id", "tables", "error", "started_at", "finished_at"}`
  with `state` one of `running`, `done`, `failed`.

`--file` paths are read by the agent (SYSTEM / root), so the CLI makes the path absolute; the
agent checks it is a regular file with its manifest next to it.

**CLI**: `sevjala restore <backup|latest> [--db NAME] [--into NAME | --replace-production]` and
`sevjala restore --file PATH [--into NAME | --replace-production]`, in the "Backups:" help group,
with Long help and examples like every other command. Watching prints one line per step change,
the loading percentage at most every 5 s, and the final summary.

**Events**: `restore_started`, `restore_source_failed` (source, reason), `copy_corrupt`,
`restore_succeeded` (source, target, duration, tables), `restore_failed` (step, error).

### Code layout (builder's guide, not binding)

- `dump/restore.go`: `Load(ctx, tools, conn, target, r io.Reader, progress)`, `CreateDatabase`,
  `DropDatabase`, `Charset(db)`, `TableCount(db)`, name validation. Reuses `Conn`, `query`,
  `explain`.
- `lanapi`: server `GET /backups/{id}`, client `GetBackup`.
- `coord/restore.go`: resolve backup and source, fetch/verify, pre-restore backup, load, state;
  a "restoring" set consulted by the backup start path.
- `localapi`: routes, `Restore` type, client methods. `cmd/restore.go`: command and watcher.
- `logx`: the five events. `store`: nothing new expected (`MarkCopy`, `Unstage` exist).

## Testing

- Unit: name validation and default name; backup/`latest` resolution; source order and fallback
  (fake LAN client); corrupt marking; replace flow order (pre-restore before drop, refusal paths:
  confirm mismatch, running backup, running restore, target exists); `--file` manifest checks;
  CLI rendering and the typed confirmation.
- Loopback integration (DoD): two in-process agents; a node serves `GET /backups/{id}`, an
  unpaired peer is refused, a damaged copy is detected.
- MariaDB container (`make test-integration`): back up a seeded database (InnoDB + MyISAM, utf8mb4,
  a routine), restore into a new database, compare `CHECKSUM TABLE` for every table (the exit
  criterion); replace-production round trip restores the original data and leaves a pre-restore
  backup.
- `doctor`: no new check (the mysql client and connection are already checked by the database
  check); stated in the PR.

## Documentation

`docs/protocol.md` (LAN and local routes, `Restore`), `docs/decisions.md` rows for each call above,
`docs/smoke-tests.md` M4 section (restore on the Windows controller, `CHECKSUM TABLE` comparison
by hand), `docs/test-walkthrough.md` restore step, README commands table, PROGRESS row.

## Left to the builder

Exact progress throttling, error wording within D-051's rules, how the watcher polls, and the
in-memory shape of the restore state.
