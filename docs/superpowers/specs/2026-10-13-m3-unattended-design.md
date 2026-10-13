# M3 Unattended — design

Date: 2026-10-13 · Milestone: M3 ([Roadmap](../../BLUEPRINT.md#roadmap)) · Branch: `p0-foundation`
(the owner keeps every milestone on one branch while the manual smokes are pending)

## Goal and exit criterion

Backups run on their own: a schedule per policy, one dump per tick tagged with every due policy,
copies kept per policy and deleted on instruction, offline nodes detected and filled in when they
come back, a full disk and a bad copy handled without the operator.

**Exit:** a 48 h run with one node powered off for 2 h. The matrix is correct, the missing copies
are backfilled, and the retention counts are exact.

Blueprint sections in play: Backup engine (policies, retention, reconciliation, transfer retry
tail), Coordination (`/status` polling), Data model (`policies` table, copy and backup states).

## Scope

In:
- New packages `scheduler` (cron and due policies) and `retention` (the pure plan).
- Changes to `store` (schema v3), `transfer`, `inventory`, `lanapi`, `coord`, `localapi`, `svc`,
  `cmd`, `config` and `logx`.
- CLI:
  - `sevjala policy list|add|set|remove`
  - `sevjala retention run [--dry-run]`
  - `sevjala backup now --tag` must name a policy
  - `sevjala backups list [--all] [--matrix]`
  - `sevjala agents` shows free space and the last problem
- LAN API: `DELETE /backups/{id}`; `GET /status` adds `store.min_free_bytes` and a low-space
  problem.
- `doctor`: `schedule` and `nodes` checks on the controller.
- Four M2 deferred minors:
  - a fingerprint pin mismatch fails at once;
  - `transfer_failed` logs the next retry;
  - a restart keeps a copy's attempt count;
  - staging files with no row are removed.

Out:
- `agents remove`.
- The `events` table (M5/M6).
- Re-hashing stored backups on a schedule; the hash is checked again before restore (M4).
- `GET /backups/{id}` (M4).
- Resumable transfer (Phase 3).

## Decisions taken in brainstorming (owner)

- **Schedule format:** standard 5-field cron plus `@hourly`, `@daily`, `@weekly`, `@monthly`.
- **Destinations:** every default policy goes to all paired nodes; a policy may name nodes instead.
  The blueprint's "nearest node" default for `frequent` is dropped.
- **Missed runs:** catch up once. A policy is due when its latest scheduled time is newer than its
  newest backup.
- **Manual backups:** a built-in `manual` policy with no schedule, keep 10, all nodes. `backup now
  --tag X` must name an existing policy.

## Policies (`store`, schema v3)

Migration 3:

```sql
CREATE TABLE policies (
  name         TEXT PRIMARY KEY,
  cron         TEXT NOT NULL,             -- '' = never scheduled
  keep         INTEGER NOT NULL,
  destinations TEXT NOT NULL DEFAULT '[]', -- JSON list of agent_id; [] = all paired nodes
  enabled      INTEGER NOT NULL DEFAULT 1
);
INSERT INTO policies (name, cron, keep) VALUES
  ('frequent', '*/10 * * * *', 12), ('hourly', '0 * * * *', 24),
  ('daily', '0 1 * * *', 30), ('monthly', '0 1 1 * *', 12), ('manual', '', 10);
```

- **Rules:**
  - The name matches the tag pattern `^[a-z0-9][a-z0-9_-]{0,31}$`.
  - `keep` is 1 to 1000.
  - `cron` must parse.
  - `destinations` must be paired node IDs.
  - `manual` cannot be removed.
- **`enabled = 0`** stops scheduling only. Retention still applies the policy's keep.
- **Queries:** `Policies`, `Policy`, `InsertPolicy` (`ErrExists` on a duplicate), `UpdatePolicy`,
  `DeletePolicy`.
- **Removing a policy never deletes a backup.** A tag with no policy keeps every backup that
  carries it (see Retention).

Existing M2 backups are tagged `manual`; they now fall under the `manual` policy.

## Cron (`scheduler`)

`scheduler.Parse(expr string) (Schedule, error)`:
- **Fields.** Five fields: minute (0-59), hour (0-23), day of month (1-31), month (1-12) and day
  of week (0-7, where 0 and 7 are Sunday).
- **Syntax.** Each field takes `*`, `n`, `a-b`, `a,b,…`, `*/n` and `a-b/n`. Names (`mon`, `jan`)
  are not accepted.
- **Aliases:**
  - `@hourly` = `0 * * * *`
  - `@daily` = `0 0 * * *`
  - `@weekly` = `0 0 * * 0`
  - `@monthly` = `0 0 1 * *`
- **The empty string** parses to a schedule that never fires.
- **Day rule.** When both day-of-month and day-of-week are restricted, a day matches if either
  matches (standard cron).
- **Errors** name the field and the bad value: `day of week "8": must be 0-7`.

Methods (local time of the controller; `t` is truncated to the minute):
- `Prev(t) (time.Time, bool)`: the latest matching minute ≤ t, searched back at most 366 days.
- `Next(t) (time.Time, bool)`: the earliest matching minute > t, searched forward at most 366 days.
- `Never() bool`.

Both walk by day, then hour, then minute, skipping whole days and hours that cannot match.
- **Spring forward.** A minute skipped by a daylight-saving change does not exist and is not
  returned.
- **Fall back.** A repeated hour is harmless: the due rule below compares against the newest backup.

`scheduler.Due(now time.Time, policies []Policy, newest map[string]time.Time) []string` returns the
due tags, sorted. `Policy{Name string; Schedule Schedule; Enabled bool}`. A policy is due when all
of these hold:
- it is enabled;
- its schedule fires;
- `f, ok := Prev(now)` is ok;
- `f` is after `newest[name]`, the creation time of the newest backup of that database carrying
  the tag (zero when none).

## Scheduler loop (`coord`)

`Controller.Tick(ctx, now)`, called at every minute boundary and once at start:
1. Do nothing when no database is configured.
2. Per configured database, compute the due tags from the store's newest backup per tag (expired
   backups count; `created_at` is the dump start).
3. Skip the database when:
   - a dump of it is running (blueprint step 1);
   - or its last failed dump is under 15 minutes old (in memory, so a restart retries at once).
4. Start one backup for that database tagged with every due policy (AD-09), through the same path
   as `backup now`. Event `schedule_fired` (database, tags, backup_id).

The first tick after install fires every scheduled policy at once, giving one backup tagged
`frequent, hourly, daily, monthly`.

## Status polling (`coord`)

`Controller.PollAll(ctx)`, called every 15 s:
- **The call.** Every paired node at once, `LAN.Status` with a 10 s timeout.
- **Results:**
  - **Success.**
    - The node becomes `warning` when its health is `warning` or `free_bytes < min_free_bytes`,
      else `online`.
    - The inventory, the free bytes, the floor and the poll start time are kept in memory.
    - `agents` gets `state`, `last_seen`, `version` and `free_bytes`.
  - **A TLS rejection** (`ErrRejected`, the node is paired with another controller) or a pin
    mismatch (`ErrPinMismatch`, the node has a new certificate) makes it `error` at once.
  - **Any other failure** counts one miss. Three misses in a row make it `offline`. Until then the
    previous state stays.
- **What is kept.** The reason of the last failure or warning is kept in memory as `problem`.
- **Polled.** An `offline` or `error` node has no usable inventory.
- **State changes** are logged once: `agent_online`, `agent_warning`, `agent_offline`,
  `agent_error` (agent_id, hostname, reason).
- **Coming back.** A node that moves from `offline`/`error`/unknown to `online`/`warning` triggers
  one `Reconcile` after the round.

## Retention and reconciliation

### The plan (`retention`, pure)

`retention.Compute(in Input) Plan`, with no I/O and no clock.

```go
type Policy struct{ Name string; Keep int; Dest []string } // Dest empty = every node
type Backup struct {
    ID, Database, SHA256 string
    Created              time.Time
    Size                 int64
    Tags                 []string
    Staged               bool              // staging file present
    Copies               map[string]string // agent_id → store copy state
    Expired              bool
}
type Held struct{ State, SHA256 string } // node's /status: verified | corrupt
type Node struct {
    AgentID         string
    Polled          bool // online or warning, inventory below is current
    PolledAt        time.Time
    FreeBytes       int64
    MinFree         int64
    Actual          map[string]Held
}
type Input struct {
    Policies    []Policy
    Backups     []Backup // non-expired and expired
    Nodes       []Node   // every paired node
    StagingKeep int
    Busy        map[string]bool // backup ids still dumping: never unstaged
    VerifiedAt  map[[2]string]time.Time // (backup, agent) → copy verified_at
}
type Plan struct {
    Updates []Update // copy state corrections from the nodes' inventories
    Deletes []Action // DELETE on a node
    Sends   []Action // queue a push from staging
    Unstage []string // remove from staging
    Expire  []string // no copy and no staging left
    Kept    []Action // left alone, with the reason (shown by --dry-run)
}
type Action struct{ BackupID, AgentID, Reason string }
type Update struct{ BackupID, AgentID, State, Reason string }
```

**Definitions.** For a node X, a database D and a policy P whose `Dest` includes X:
- **`newest(D, P)`**: backups of D carrying tag `P.Name`, newest `Created` first.
- **`wanted(X, D, P)`**: the first `P.Keep` of `newest(D, P)`.
- **`held(X, D, P)`**: the first `P.Keep` of `newest(D, P)` that are verified on X. Verified on X
  means X's `Actual` lists it `verified` with the backup's `SHA256`.
- **`needed(X)`**: the union over every such (D, P) of `held` and `wanted`, plus every backup that
  carries a tag with no policy.

**Rules, for each polled node X:**
1. **Copy corrections.**
   - `Actual` verified with the right hash, copy not `verified` (or no copy row): Update to
     `verified`.
   - `Actual` corrupt, or verified with another hash: Update to `corrupt`.
   - Copy `verified` and missing from `Actual`, verified at least 1 s before `PolledAt`: Update to
     `failed`, reason `missing from the node`. A copy verified during the poll is not touched.
2. **Unknown backups.** An ID in `Actual` that the store does not know: Kept, reason `not made by
   this controller`. It is never deleted.
3. **Deletes.** A backup in `Actual` and not in `needed(X)`: Delete, reason `retention: not among
   the newest <keep> <tag> on this node` (the tag that held it last, or `no policy sends it here
   any more`).
4. **Corrupt and needed.** A corrupt backup in `needed(X)`:
   - when `Staged`: Delete (reason `corrupt: replaced from staging`), then Send;
   - otherwise: Kept, reason `corrupt, no staging copy to replace it`.
5. **Sends.** A backup in `wanted(X, …)` that is not verified on X and not handled by rule 4:
   - not `Staged`: Kept, reason `missing, no staging copy`;
   - `FreeBytes − Size < MinFree`: Kept, reason `waiting for free space`;
   - otherwise: Send, reason `backfill`.

**Nodes not polled** get no Updates, Deletes or Sends. They are listed once in Kept with reason
`node offline`.

**Staging**, computed over every paired node, polled or not. A staged backup stays when any of
these holds:
- it is among the newest `StagingKeep` staged backups of its database;
- it is `Busy`;
- some node X has it in `wanted(X, …)` and the copy on X is not `verified`.

Every other staged backup goes to Unstage.

**Expire.** A non-expired backup becomes expired when both hold:
- after the plan, it has no copy in `verified`, `corrupt`, `pending` or `transferring`;
- it is not staged (or is being unstaged).

The result is deterministic: lists sorted by backup ID, then agent ID.

### Applying the plan (`coord`)

`Controller.Reconcile(ctx, dryRun bool) (localapi.RetentionPlan, error)`, every 5 min, after a node
comes back, and from `retention run`. A mutex makes runs exclusive.

1. **Build the input:**
   - from the store: policies, backups, copies and agents;
   - from the poll cache: `Actual`, `PolledAt`, free space;
   - from the staging dir: `Staged`;
   - from the in-memory jobs: `Busy`;
   - and `StagingKeep` from config. The default is now 12, and 0 in a config file means 12.
2. **Compute.** A dry run returns the plan view here.
3. **Updates.** Apply the Updates with `Store.SetCopy`.
4. **Deletes**, in order, per node: `LAN.DeleteBackup(addr, fp, id, reason)`.
   - On success the copy becomes `deleted`. Event `retention_delete` (backup_id, agent_id,
     reason).
   - A failure is logged and left for the next run.
5. **Sends.**
   - `Store.EnsureCopy(id, agent)` creates the copy, or resets it to `pending` from `failed`,
     `offline`, `corrupt` or `deleted`.
   - `Pool.Enqueue`.
   - Event `backfill_queued`.
6. **Unstage.** Remove `<id>.sql.gz` and `<id>.json`, then `Store.Unstage(id)` sets
   `staging_path = ''`. Event `staging_pruned`.
7. **Orphans.** Also remove staging files whose ID has no backup row and no running job.
8. **Expire.** `Store.Expire(id)` sets state `expired`.
9. **Kept.** Kept entries with reason `not made by this controller` log `unknown_backup` once per
   agent and ID for the agent's lifetime.

While any paired node is `offline`, each 5-minute run first calls `Discover`. It refreshes the
address of a paired node whose fingerprint matches (D-015), so a DHCP change heals.

### Backup and copy states (`store`)

- **Copy states:** `pending`, `transferring`, `verified`, `failed`, `offline`, `corrupt`,
  `deleted`.
- **The backup state** is recomputed whenever a copy, the staging path or expiry changes:

  | State | When |
  |---|---|
  | `created` | any copy `pending` or `transferring`, or no copy other than `deleted` and still staged |
  | `distributed` | every copy other than `deleted` is `verified` (at least one) |
  | `partial` | otherwise, with at least one copy other than `deleted` |
  | `expired` | set only by `Expire`; never recomputed away |

- **New queries:**
  - `EnsureCopy`, `Unstage`, `Expire`.
  - `SetAgentState(id, state, freeBytes, version, seen)`.
  - `Backups` gains a filter for expired rows.
- **`Unsettled`** keeps returning `pending` and `transferring` copies with their `attempts`.

## Transfer (`transfer`)

- **Dedupe.** `Enqueue` drops a job whose (backup, agent) is already queued or running.
- **Job.** `Job{BackupID, AgentID, Attempts}`, where `Attempts` is the count already made; `Resume`
  passes the stored value. Recorded attempts continue from it; the backoff delays restart at 30 s.
- **Settle.** `Settle(Job, Update)` with `Update{State string; Attempts int; Err error; Retry
  time.Duration}`. `Retry` is the wait before the next attempt, 0 when none. `transfer_failed`
  logs it as `next_retry`.
- **Status codes:**
  - **507** is `Permanent` in `coord.Push`: the copy is `failed`. The plan sends it again once the
    node reports enough free space.
  - **409, 400, 403**, a TLS rejection and a pin mismatch stay permanent.
  - **422** is retried.
- **After the last retry** the copy is `failed` or `offline` as in M2. The 5-minute reconcile
  replaces the blueprint's "then once per `/status` cycle": it sends any wanted copy that is still
  missing.

## Node (`inventory`, `lanapi`)

`inventory.Store.Delete(id) (bool, error)`:
- `ErrInvalid` for a bad ID; `ErrBusy` while that ID is being received.
- Otherwise it removes `<id>.sql.gz`, then `<id>.json`, then syncs the dir. It returns whether
  anything existed.

`DELETE /backups/{id}` (paired controller only):
- **Body.** `{"reason":"…"}`, at most 4 KiB; an empty reason is 400.
- **Responses:**

  | Status | Meaning |
  |---|---|
  | 200 | `{"backup_id","deleted":true\|false}` (absent is not an error) |
  | 400 | bad ID or no reason |
  | 503 | being received |
  | 409 | this agent is a controller |

- **Event.** `backup_deleted` (backup_id, reason, remote, deleted).

`GET /status`:
- `store` adds `min_free_bytes`.
- Free space below it adds the problem `free space <n> is below the floor <m>` (health
  `warning`).

Client:
- `DeleteBackup(ctx, addr, nodeFP, id, reason) (bool, error)`.
- `ErrPinMismatch`, wrapped by the transport's pin check, so `errors.Is` finds it through the TLS
  error.

## Local API

| Route | Request → response |
|---|---|
| `GET /policies` | → `{"policies":[Policy…]}` |
| `POST /policies` | `{name, schedule, keep, to:[agent…]}` → `Policy`; 400 invalid, 409 exists |
| `PATCH /policies/{name}` | `{schedule?, keep?, to?, enabled?}` → `Policy`; 404 unknown |
| `DELETE /policies/{name}` | → `{}`; 404 unknown, 409 for `manual` |
| `POST /retention/run` | `{dry_run}` → `RetentionPlan` |
| `GET /backups?all=1` | expired backups included |
| `GET /agents` (changed) | each agent adds `free_bytes`, `problem` |

- **`Policy`:** `{name, schedule, keep, to:[{agent_id, hostname}], enabled, next_run}`.
  - `to: []` means all nodes.
  - `next_run` is omitted when the policy never fires or is disabled.
- **`to` entries** in requests are agent references resolved like every other `<agent>` (D-014)
  against paired nodes. `["all"]` or `[]` means all.
- **`RetentionPlan`:** `{dry_run, deletes, sends, unstaged, expired, kept}`. Each action is
  `{backup_id, agent_id, hostname, reason}`.

Event `policy_changed` (name, what: added/changed/removed).

## CLI

**`sevjala policy list`**
- Table `NAME SCHEDULE NEXT RUN KEEP TO ENABLED`.
- `TO` is `all` or hostnames. `NEXT RUN` is local time or `-`.

**`sevjala policy add <name> [--schedule CRON] [--keep N] [--to all|host,…]`**
- `--keep` is required. No `--schedule` means the policy runs only through `backup now --tag`.

**`sevjala policy set <name> [--schedule] [--keep] [--to] [--enable|--disable]`**
- Only the flags given change anything.

**`sevjala policy remove <name>`**
- Prints that existing backups with the tag are kept.

**`sevjala retention run [--dry-run]`**
- Prints the plan grouped as `delete`, `send`, `unstage`, `expire`, `keep`, one line each:

  ```
  delete  BK-20261013-010000-A8F2  BACKUP-PC-02  retention: not among the newest 30 daily on this node
  send    BK-20261014-011000-1C9D  BACKUP-PC-03  backfill
  keep    BK-20260101-010000-0001  BACKUP-PC-01  not made by this controller
  ```

- `nothing to do` when empty. Without `--dry-run` it runs the plan and prints the same.

**`sevjala backups list`**
- Hides expired backups unless `--all`.
- `--matrix` cells:

  | Copy state | Cell | ASCII |
  |---|---|---|
  | verified | `✓` | `v` |
  | pending, transferring | `…` | `.` |
  | failed | `✗` | `x` |
  | offline | `○` | `o` |
  | corrupt | `!` | `!` |
  | deleted | `-` | `-` |

**`sevjala agents`**
- Adds `FREE` (human bytes) and `PROBLEM` columns.

**`sevjala backup now --tag X`**
- 400 with the policy names when X is not a policy.

## Agent wiring (`svc`, `coord`)

- **Start.** `Controller.Run(ctx)` starts after `Resume` and stops with the agent context. Shutdown
  waits for it before closing the pool.
- **Loops:**
  - `Tick` at start, then at every minute boundary.
  - `PollAll` at start, then every 15 s.
  - `Reconcile` 30 s after start (after the first poll rounds), then every 5 min.
- **Intervals** are fields on the controller, for tests; they are not config.

The loops live in `coord`, not in a separate `reconcile` package: the poll cache and the plan's
apply step need the controller's store, pool and LAN client, which `coord` already holds (D-016).

## Doctor (controller)

- **`schedule`:**
  - WARN when no database is configured (the scheduler is idle), or when no enabled policy has a
    schedule.
  - Otherwise it shows the next run and its policies.
- **`nodes`:**
  - WARN listing each node that is `offline` or `error`, with its problem.
  - FAIL when there is no paired node.

## Events (provisional until the PRD event list is in the repo)

| Side | Events |
|---|---|
| Controller | `schedule_fired`, `policy_changed`, `agent_online`, `agent_warning`, `agent_offline`, `agent_error`, `backfill_queued`, `retention_delete`, `staging_pruned`, `unknown_backup`; `transfer_failed` gains `next_retry` |
| Node | `backup_deleted` |

## Testing

**Unit tests:**
- `scheduler`:
  - Parse: valid, invalid with messages, aliases, empty.
  - `Prev`/`Next`: each default; month ends; Feb 29; the day-of-month/day-of-week OR rule; a
    daylight-saving gap and repeat (`time.LoadLocation("Europe/Berlin")`, skipped when tzdata is
    missing).
  - `Due`: never, disabled, the catch-up case, already-made.
- `retention` (table-driven; the core of the milestone):
  - per-node keep;
  - "new backups not arriving keeps the old ones";
  - backfill;
  - corrupt with and without staging;
  - wrong hash;
  - unknown backup;
  - a tag without a policy;
  - a destination change;
  - an offline node;
  - waiting for space;
  - a copy verified during the poll;
  - staging keep, busy and pending;
  - expire;
  - deterministic ordering.
- `store`: v2 → v3 migration with seeded policies; policy CRUD; `EnsureCopy` from each state; the
  recomputed backup states, including deleted and expired; `Unstage`; `SetAgentState`.
- `transfer`: dedupe; attempts carried over; `Retry` reported; 507 permanent.
- `inventory`: Delete (present, absent, busy, bad ID, data only, manifest only).
- `lanapi`:
  - DELETE: ok, absent, no reason 400, unpaired 403, busy 503, controller 409.
  - Status `min_free_bytes` and the low-space problem.
  - A pin mismatch is `ErrPinMismatch`.
- `coord`:
  - Tick: due, skipped while running, retry after a failure.
  - PollAll with a fake LAN: three misses make it offline, a rejection makes it error, a return
    triggers a reconcile.
  - Policy validation and `to` resolution.
  - `backup now --tag` validation.
- `localapi` and `cmd`: the new routes, renderers, flags.

**Loopback integration** (`coord`): a controller, 3 in-process nodes, a fake dumper and policies of
keep 2, driven by explicit `Tick`/`PollAll`/`Reconcile` calls with a fake clock:
- after 5 ticks every node holds exactly 2 per tag;
- a stopped node becomes offline after 3 polls, and misses 2 ticks;
- once restarted, the reconcile backfills it to exactly 2 per tag;
- a corrupted node file is deleted and replaced;
- staging keeps the newest 2 (`StagingKeep` 2) plus what is pending.

**Docker E2E (accelerated exit test):**
- MariaDB plus a controller and 3 nodes.
- Policies `*/1 * * * *` keep 3 and `*/2 * * * *` keep 2.
- One node paused for 10 minutes.
- Afterwards: the matrix is correct, the node is backfilled, and every node holds exactly the
  counts. `retention run --dry-run` prints `nothing to do`.

**Manual** (`docs/smoke-tests.md`): the real 48 h run with one node powered off for 2 h; a node disk
filled below its floor; a node backup file truncated by hand.

## Docs touched

- `docs/protocol.md`: `DELETE /backups/{id}`, the `/status` additions, the local routes, schema v3,
  the copy and backup states.
- `docs/decisions.md`: D-031 onwards for cron, destinations, the due rule and failure delay, the
  manual policy, per-node retention, unknown and policy-less backups, backfill replacing the status
  cycle and 507, corrupt replacement, staging keep per database and orphans, the loops in `coord`,
  and discovery while a node is offline.
- `docs/BLUEPRINT.md`: an **Amended** note on the policy defaults table (all nodes, the `manual`
  row), pointing at the decisions.
- `docs/smoke-tests.md` (M3), `docs/PROGRESS.md`, `README.md`.
