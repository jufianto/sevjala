# M2 First backup — design

Date: 2026-10-13 · Milestone: M2 ([Roadmap](../../BLUEPRINT.md#roadmap)) · Branch: `p0-foundation`
(the owner keeps every milestone on one branch while the manual smokes are pending)

## Goal and exit criterion

The Controller dumps a MySQL/MariaDB database once, hashes it once, keeps a staging copy, and
pushes it to every paired Node. A Node stores it only after the hash and size match.

**Exit:** a 300 MB XAMPP database lands `verified` on 3 nodes. A transfer killed mid-way leaves
no `.part` after cleanup.

Blueprint sections in play: Backup engine (detection, dump, transfer), Data model (manifest,
node store, backups tables), Security model (secrets, backup files).

## Scope

In:
- New packages `dump`, `transfer` and `inventory`.
- Changes to `store` (schema v2), `lanapi`, `coord`, `localapi`, `config` (database section and
  secrets), `svc`, `cmd` and `logx`.
- CLI:
  - `sevjala db detect`
  - `sevjala db set`
  - `sevjala backup now [--db X] [--tag T]`
  - `sevjala backups list [--matrix]`
- LAN API: `PUT /backups/{id}`, and `GET /status` now includes `store` and `backups`.
- `doctor` checks: the dumper and the database (controller), the store dir and free space (node).
- **Pairing fix (I-5, from the M1 review).** `sevjala code` shows the node's fingerprint, and
  `pair` checks it before sending the proof.

Out:
- Scheduler, policies, retention and staging pruning (M3).
- `/status` polling, offline detection and backfill (M3).
- `GET /backups/{id}` and `DELETE /backups/{id}` (M4 and M3).
- The `events` table (M5/M6).
- Disk-full handling beyond the upfront free-space check (M3).

## Decisions taken in brainstorming (owner)

- **Pairing:** fix I-5 now with an out-of-band fingerprint check.
- **`backup now`:** waits and shows progress. Ctrl-C stops watching, not the backup.
- **Targets:** every backup goes to every paired node; M3 policies narrow this later.
- **DB password:** the agent stores it in the OS keyring itself.
  - It runs as SYSTEM/root, so the CLI sends the password over the local pipe.
  - Windows: Credential Manager. macOS: keychain (go-keyring drives `security -i` through
    stdin). Linux: Secret Service, else the blueprint's AES-GCM file fallback.
  - An empty password needs no entry.

## Pairing fix (I-5)

- **The gap.** A device on the LAN can answer `discover` for a node. The controller then pins
  that device's fingerprint and sends it the HMAC proof. The device brute-forces the 6-digit
  code offline and becomes a "node" that receives database dumps.
- **The fix.** Check the fingerprint out of band.

**Node side.**
- Local `POST /code` adds `fp` to its response.
- `sevjala code` prints the code and the first 16 hex characters of the fingerprint (64 bits).
  It also prints the controller command: `sevjala pair <hostname> --code 482913 --fp 3f2a9b1c77de0a41`.

**Controller side.**
- Local `POST /pair` takes `{"agent","code","fp"}`.
- Parsing `fp`:
  - A leading `sha256:` is dropped.
  - Spaces, dashes and colons are dropped.
  - Matching ignores case.
  - At least 16 hex characters are required, else 400.
- The controller compares `fp` with the start of the cached fingerprint before any network
  call. A mismatch returns 409:
  - The message: `the agent found at <addr> has fingerprint <16 hex>, not the one shown by
    'sevjala code'; do not pair: another device may be answering for it`.
  - The event: `pair_rejected` with reason `fingerprint mismatch`.
- The TLS client already pins the cached fingerprint. An impostor would need a certificate
  whose SHA-256 matches 64 chosen bits, which is out of reach.
- This is a decisions row, not an AD change: AD-06 already says "pinned fingerprints".

## Configuration and secrets (`config`)

`[controller.database]`:

| Key | Default |
|---|---|
| `host` | `127.0.0.1` |
| `port` | 3306 |
| `user` | `root` |
| `password_ref` | `""` (no password); `keyring:db` once one is set |
| `dumper` | `""` (auto-detect) |
| `databases` | `[]` |

`[node.store]`:

| Key | Default |
|---|---|
| `path` | `""`, meaning `<data-dir>/store`, which inherits the locked ACL |
| `min_free_bytes` | 2147483648 |

- **Writing.** `config.Write` writes the whole file with the TOML encoder: temp file, then
  rename, mode 0644. The agent is the only writer, through `db set`; the data dir is not
  writable by the CLI user.
- **Secrets.**
  - API: `config.SetSecret(dataDir, name, value)`, `config.Secret(dataDir, ref)` and
    `config.DeleteSecret(dataDir, name)`. The keyring service is `sevjala`, the account is the
    name.
  - Linux fallback, used when the keyring call fails (no D-Bus session under systemd):
    - The value is AES-256-GCM encrypted in `<data-dir>/secrets/<name>.enc`.
    - The key is 32 random bytes in `<data-dir>/secrets/key`, mode 0600.
    - Event `keyring_fallback`, a warning.
  - Get order: keyring first, then the file.
  - Tests use `keyring.MockInit()`.
- **Never seen.** The password does not appear in config, logs, `/status`, error messages or
  test fixtures.

## Database detection (`dump`)

`FindTools(cfg) (Tools, error)` with `Tools{Dumper, Client string}` looks in this order:
1. The configured `dumper`.
2. XAMPP:
   - Windows: `C:\xampp\mysql\bin\mysqldump.exe`
   - macOS: `/Applications/XAMPP/xamppfiles/bin/mysqldump`
   - Linux: `/opt/lampp/bin/mysqldump`
3. `mysqldump` on `PATH`, then `mariadb-dump`.

The client is `mysql`(`.exe`) next to the dumper, else `mariadb`. Detection and restore (M4)
use the client binary instead of a Go MySQL driver, which keeps the dependency list to the
blueprint's.

`Detect(ctx, tools, conn) (Detection, error)` runs the client with `-N -B -e`:
- `SELECT VERSION()` gives the server version. "MariaDB" in it gives the flavour.
- `SHOW DATABASES` lists databases, minus `information_schema`, `performance_schema`, `mysql`
  and `sys`.
- Per database, base tables and their engine come from `information_schema.TABLES`. Any table
  whose engine is not InnoDB means `--lock-tables` (listing those tables); otherwise
  `--single-transaction`.

`conn` holds host, port, user and password. The password reaches every child process only
through the `MYSQL_PWD` environment variable: never on the command line, never on disk.
Connection failures return the client's stderr with any `MYSQL_PWD` value scrubbed.

## Dump pipeline (`dump`)

`Run(ctx, opts) (Result, error)`:

1. **Allocate the ID** `BK-<yyyymmdd>-<hhmmss>-<4 uppercase hex>` (local time of the
   controller), and open `staging/<id>.part` with `O_EXCL`.
2. **Pick the consistency flag** by re-reading the engines just before the dump.
3. **Run the dumper:**
   ```
   <dumper> --host --port --user --routines --triggers --events --hex-blob --no-create-db
            --default-character-set=utf8mb4 <consistency> <db>
   ```
   - stdout goes through `gzip.Writer` into `io.MultiWriter(file, sha256)`.
   - stderr is captured, capped at 64 KiB.
   - `--default-character-set=utf8mb4` is added beyond the blueprint so 4-byte characters
     survive an older client default (decisions row).
4. **Abort** on a non-zero exit, on stderr containing `error` (case-insensitive), or on a
   cancelled context. Abort means delete the `.part` and return the error with the stderr
   tail.
5. **Finish:**
   - Close gzip, `Sync`, close.
   - Rename to `staging/<id>.sql.gz`.
   - Write `staging/<id>.json` (temp, sync, rename) and sync the dir (Unix).
   - Return the manifest.

Manifest (blueprint format, identical on the controller and the nodes) is `inventory.Manifest`:
`v, backup_id, database, created_at (RFC 3339 with offset), source_agent, server_version,
dump_flags (consistency and content flags; no host, port or user), size, sha256, tags`.

At agent start, a controller deletes every `staging/*.part`; no dump survives a restart.

## Node store (`inventory`)

`inventory.Open(path, minFree, log)` creates the dir (0700 on Unix) and deletes every `*.part`
in it. `Receive(ctx, m Manifest, sha, size, body) error`:

1. **Validate the request:**
   - The ID matches `^BK-\d{8}-\d{6}-[0-9A-F]{4}$`, so no path can be smuggled in.
   - The manifest's `backup_id`, `sha256` and `size` equal the path and headers.
2. **Check existing state:**
   - The ID already stored with the same sha256: return `ErrExists`. The handler answers 200
     without reading the body.
   - Stored with another sha256, or the same ID in flight: `ErrConflict` (409).
3. **Check space:** free bytes minus size below `min_free_bytes` gives `ErrNoSpace` (507).
4. **Write** `<id>.part` (`O_EXCL`), copying while hashing.
5. **On EOF:** size and hash match means:
   - `Sync`, close.
   - Write sidecar temp, rename the `.part` to `<id>.sql.gz`, then rename the sidecar to
     `<id>.json`, then sync the dir.
   - Return nil. Any mismatch, read error or cancel deletes the `.part`; a mismatch returns
     `ErrMismatch` (422).

`Scan(now) (Inventory, error)` lists the dir:

| File state | Reported state |
|---|---|
| A `.sql.gz` with a sidecar whose size matches | `verified` |
| Size differs, or a sidecar without its file | `corrupt` |
| A file without a sidecar | `corrupt` (a crash between the two renames), plus a problem |

`*.part` older than 1 h and not in flight is deleted and reported once as a problem. Free bytes
come from `statfs` (Unix) or `GetDiskFreeSpaceEx` (Windows), both in `x/sys`.

The store never re-hashes on scan; the full re-hash happens before restore (M4).

## LAN API (`lanapi`)

`PUT /backups/{id}` (paired controller only, through `pairedOnly`):
- **Headers:**
  - `X-Sevjala-Sha256`: 64 lowercase hex.
  - `X-Sevjala-Size`.
  - `X-Sevjala-Manifest`: base64 JSON, header capped at 8 KiB.
  - `Expect: 100-continue`, so a rejection costs no body bytes.
- **Responses:**

  | Status | Meaning |
  |---|---|
  | 200 | `{"backup_id","sha256","size"}` (also for an identical existing copy) |
  | 400 | bad headers or manifest |
  | 409 | conflict |
  | 422 | mismatch |
  | 507 | no space |
  | 500 | write error |

- **Stalls.** The handler extends the read deadline by 60 s on each read
  (`http.ResponseController`), so a stalled controller frees the slot.
- **Events.** `backup_received` (id, size); `backup_rejected` (id, reason).
- **Role.** A controller answers this route with 409.

`GET /status` adds `"store": {"path","free_bytes"}` and fills `backups` (`backup_id, size,
sha256, state`) and `problems` from `Scan`. `health` is `healthy` without problems, else
`warning`. A controller's `/status` keeps `store` absent.

Client: `PutBackup(ctx, addr, nodeFP, m, file io.Reader) (sha256 string, err)`.
- No overall timeout, `ExpectContinueTimeout` 5 s.
- A watchdog cancels the request when no bytes have moved for 60 s.

## Controller store (`store`, schema v2)

The blueprint tables, as migration 2:

```sql
CREATE TABLE backups (backup_id TEXT PRIMARY KEY, database TEXT NOT NULL, created_at TEXT NOT NULL,
  size INTEGER NOT NULL, sha256 TEXT NOT NULL, tags TEXT NOT NULL, staging_path TEXT NOT NULL,
  state TEXT NOT NULL);                     -- created | distributed | partial  (expired: M3)
CREATE TABLE backup_copies (backup_id TEXT NOT NULL REFERENCES backups, agent_id TEXT NOT NULL,
  state TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '',
  verified_at TEXT NOT NULL DEFAULT '', PRIMARY KEY (backup_id, agent_id));
```

- **Queries:** `InsertBackup` (with its pending copies, one transaction), `Backups`, `Backup`,
  `SetCopyState` and `Unsettled`. `Unsettled` lists copies in `pending` or `transferring`, for
  requeue at start.
- **Backup state** (`created` → `distributed` | `partial`) is recomputed whenever a copy
  settles:
  - `distributed`: every copy is `verified`.
  - `partial`: none is `pending` or `transferring` and at least one is not `verified`.

## Transfer (`transfer`)

- **Shape.** `transfer.Pool`: one worker goroutine and FIFO queue per node agent_id, created on
  first use. The pool knows nothing of HTTP: `Push func(ctx, Job) error` and
  `Settle func(Job, state, attempts, err)` are fields, filled by `coord`.
- **Per job:**
  - `transferring`, then push.
  - Success gives `verified`.
  - A failure retries after 30 s, 2 min and 10 min (the delays are a field, for tests).
  - After the last retry the copy is `failed`, or `offline` when every attempt failed to
    connect.
  - From M3 the `/status` cycle keeps retrying.
- **Status codes:** 409 (conflict) ends the job as `failed` with no retry. 422 (mismatch) is
  retried, since the bytes may have been damaged in flight.
- **Stopping.** `Pool.Close` cancels in-flight pushes: the node deletes the `.part`, and the
  copy stays `transferring` and is requeued at the next start.

## Controller orchestration (`coord`)

`Controller.BackupNow(ctx, db string, tags []string) ([]localapi.BackupJob, error)`:
- **Which databases.** `db` empty means every configured database, one backup each, in
  sequence. A db not configured is 400, with the configured list.
- **One at a time.** A dump already running for a database is 409.
- **Tags.** Default `["manual"]`. Tags are stored now and given meaning by M3.
- **Returns** at once with the allocated IDs. Work continues in a goroutine on the agent's
  lifetime context.
- **In flight.** In memory: an ID is `dumping` (with bytes written so far) or `failed`
  (error). After the dump it is in the store, with one `pending` copy per paired node, and is
  queued.
- **No paired nodes.** The backup stays in staging (recovery point zero) and the job says so.

`Backups(ctx)` and `Backup(ctx, id)` merge the store rows with the in-memory jobs. They return
`localapi.Backup{backup_id, database, created_at, size, sha256, tags, state, error, bytes,
copies:[{agent_id, hostname, state, attempts, last_error, verified_at}]}`.

`SetDatabase(ctx, req)` (local `PUT /db`):
1. Applies the given fields over the current config.
2. Resolves the tools and runs `Detect` with the new connection. Failure is 400 with the cause,
   and nothing is saved.
3. Checks every listed database exists.
4. Stores the password (when given) as `keyring:db`; an empty password deletes the secret and
   clears the ref.
5. Writes `config.toml` and swaps the in-memory config under a mutex.

Event `db_configured` (host, port, user, databases; never the password).

`DetectDatabase(ctx)` (local `POST /db/detect`) runs `Detect` with the current config.

## Local API

| Route | Role | Request → response |
|---|---|---|
| `POST /code` (changed) | node | adds `fp` |
| `POST /pair` (changed) | controller | body adds `fp` |
| `POST /db/detect` | controller | → `Detection{dumper, client, server_version, flavour, databases:[{name, tables, consistency, non_innodb:[…], configured}]}` |
| `PUT /db` | controller | `{host?, port?, user?, password?, dumper?, databases?}` → `Detection` |
| `POST /backup/now` | controller | `{db?, tags?}` → `{"backups":[{backup_id, database}]}` |
| `GET /backups` | controller | → `{"backups":[Backup…]}`, newest first |
| `GET /backups/{id}` | controller | → `Backup`; 404 unknown |

Local `GET /status` on a node adds `store` (path, free_bytes, backups count, problems), which
`doctor` reads.

## CLI

**`sevjala code`**
- Prints the code, the expiry and `fingerprint 3f2a9b1c77de0a41`.
- Prints the full controller command.

**`sevjala pair <agent> --code N --fp F`**
- `--fp` is required; under 16 hex characters is exit 2.

**`sevjala db detect`**
- Shows the dumper, client and server version.
- Table `DATABASE TABLES MODE CONFIGURED`, where `MODE` is `single-transaction` or
  `lock-tables (MyISAM: t1, t2)`.

**`sevjala db set`**
- Flags: `--host`, `--port`, `--user`, `--dumper` and `--db a,b`.
- `--password` prompts without echo (`golang.org/x/term`, decisions row).
- `--password-stdin` reads one line.
- `--no-password` clears it.
- Only the flags given change anything. Prints the detection on success.

**`sevjala backup now [--db X] [--tag T]…`**
- Starts the backup, then polls `GET /backups/{id}` every second and prints each transition,
  for example:

  ```
  BK-20261013-101500-A8F2 production_db: dumping… 312 MB
  BK-20261013-101500-A8F2: created 298 MB sha256:a24b…
    BACKUP-PC-01 verified · BACKUP-PC-02 transferring · BACKUP-PC-03 failed (retry 1 in 30s): …
  ```

- Exit 0 when every copy is `verified`.
- Exit 1 when the dump failed, or any copy ended `failed`/`offline`.
- Ctrl-C prints `still running in the agent; see 'sevjala backups list'` and exits 130.

**`sevjala backups list`**
- Table `BACKUP DATABASE CREATED SIZE TAGS STATE COPIES` (`2/3 verified`).
- `--matrix`: rows are backups, columns are node hostnames, cells show `✓` verified, `…`
  pending/transferring, `✗` failed, `○` offline. Plain ASCII under Windows code pages
  (`v . x o`), decided by `GOOS`.

## Doctor

- **Controller:**
  - `dumper` (found path).
  - `database`: detect succeeds through the agent; reports the server version and the
    consistency mode per configured database. WARN when none is configured.
  - `staging` (dir writable).
- **Node:**
  - `store`: path and free bytes. FAIL under `min_free_bytes`; WARN when problems were reported.

## Events (provisional until the PRD event list is in the repo)

| Side | Events |
|---|---|
| Controller | `backup_started` (id, db), `backup_created` (id, size, sha256), `backup_failed` (id, reason), `transfer_started`, `transfer_verified`, `transfer_failed` (attempt, err, next retry), `db_configured`, `keyring_fallback` |
| Node | `backup_received`, `backup_rejected`, `part_removed` (id, age) |

No event carries the password.

## Testing

**Unit tests:**
- `dump`:
  - A fake dumper/client is the test binary re-executed with an env var selecting its
    behaviour (cross-platform, no shell scripts).
  - It covers: tool search order; consistency choice; a successful dump (gunzipped content and
    sha256 match the manifest); a non-zero exit; stderr `error`; cancel deletes the `.part`;
    `MYSQL_PWD` set and absent from argv; the ID format.
- `inventory`:
  - Receive ok; mismatched hash or size deletes the `.part`; an aborted body deletes the
    `.part`; same ID same hash gives `ErrExists`; another hash gives conflict.
  - In-flight duplicate; bad IDs (`../x`, lowercase); no space.
  - Scan states (verified, size mismatch, sidecar only, file only); stale `.part` removal with
    a fake clock; `Open` removes `.part`.
- `store`: v1 → v2 migration on an existing v1 db; insert/list; copy state transitions
  recompute the backup state; unsettled.
- `transfer`: success, retry then success, retries exhausted gives `failed`/`offline`, 409 not
  retried, close cancels, one worker per node runs jobs in order (fake push, millisecond
  delays).
- `config`: full write/load round trip; secret set/get/delete with `MockInit`; Linux file
  fallback encrypt/decrypt; a wrong key fails.
- `lanapi`: put ok, put without the paired cert is 403, mismatch 422, identical re-put 200 with
  no body sent, bad headers 400, controller 409; `/status` lists backups.
- `localapi`: new routes against the fake backend.
- `cmd`: renderers, matrix, password flags.
- `coord`: fingerprint check (match, mismatch 409, short 400).

**Loopback integration** (`coord`):
- Controller plus 3 in-process nodes and a fake dumper: `BackupNow`, then all 3 copies
  `verified`.
- The controller's push is cancelled mid-stream: the node has no `.part`, and the copy is
  requeued and verified.

**`make test-integration`** (Docker; build tag `integration`):
- Starts `mariadb:10.4` (XAMPP's MariaDB 10.4.32 line).
- Seeds an InnoDB database and a mixed MyISAM database.
- Cross-compiles the test binaries (`CGO_ENABLED=0`, the Docker host's arch) and runs them
  inside the container, which already has `mysqldump`/`mysql`.
- Checks real detection, a real dump of both, and the restore of the gunzipped dump into a new
  database with matching row counts.

**Manual** (`docs/smoke-tests.md`): a 300 MB XAMPP DB to 3 real PCs; kill a node agent mid-transfer
and check no `.part` remains after its restart; Win7 `db set` with a password (Credential
Manager as SYSTEM); macOS keychain as root.

## Docs touched

- `docs/protocol.md`: manifest, node store dir, `PUT /backups`, full `/status`, local routes,
  schema v2, the `/pair` `fp`.
- `docs/decisions.md`: D-018 onwards for the pairing fingerprint check, tools via the client
  binary, `MYSQL_PWD`, the utf8mb4 flag, `x/term`, store default path, tags default,
  in-memory jobs, retry tail before M3, and the config/secrets location.
- `docs/smoke-tests.md` (M2), `docs/PROGRESS.md`, `README.md`.
