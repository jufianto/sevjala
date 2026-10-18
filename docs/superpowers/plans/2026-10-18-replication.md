# Replication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. The owner has so far run plans inline with superpowers:executing-plans, one atomic commit per task on `master`.

**Goal:** A paired backup PC becomes a read-only MariaDB replica of the controller's databases with two commands, and every screen on both PCs says whether it keeps up (AD-22..AD-24).

**Architecture:** A new package `replica/` holds what both roles share: the `my.ini` editor, the server-id rule, the `SHOW SLAVE STATUS` parser, the firewall rule and the node-side `replica.Node`. `replica.Node` prepares the local MariaDB, receives and loads the seed, starts or stops replication, promotes, and every 15 s fills the `replica` block of `lanapi.Status`. `coord/replica.go` runs setup, resync and remove on the controller: binary log, replication user, firewall, one consistent multi-database seed dump through `dump.Seed`, then `PUT /replica/seed` and `PUT /replica` to the node. The health rules sit in `coord/health.go` and feed `status`, the tray and a dashboard card.

**Tech Stack:** Go stdlib, cobra, the mysql/mysqldump client binaries through `dump`, `go-keyring` through `config.SetSecret`, Svelte 5, and Docker MariaDB for the integration test.

**Spec:** `docs/superpowers/specs/2026-10-18-replication-design.md`

## Global Constraints

- MariaDB only on both sides. MySQL is refused with `Replication setup supports MariaDB only; <host> runs MySQL <version>`. The replica's version must be the same as the server's or newer (major.minor).
- One replica per controller. The repl user is `sevjala_repl`. The keyring holds `replica-repl` (controller) and `replica-db` (node). The firewall rule is `Sevjala-Replica-MySQL`. Seed files are `staging/replica-seed.sql.gz[.part]` and `<data>/replica/seed.sql.gz[.part]`.
- `my.ini` edits go under `[mysqld]`, after a one-time copy to `<path>.sevjala-bak`.
  - Server: `log-bin=mysql-bin`, `binlog_format=ROW`, `expire_logs_days=7`, and `server-id=1` if `@@server_id` is 0. Comment out an active `skip-networking` or `bind-address=127.0.0.1`.
  - Replica: `server-id=<derived>`, `read_only=1`, `skip-slave-start`, `relay-log=mysql-relay-bin`.
- Derived server id: `1000 + binary.BigEndian.Uint32(sha256(agentID)[:4]) % 1_000_000_000`.
- Thresholds: watcher every 15 s. Warning when more than 300 s behind, or when the IO thread is not `Yes` for more than 120 s. A setup that is following the node polls every 2 s, and fails after 10 min with no progress.
- Health texts, CLI texts and confirm words (`pause`, `promote`) are exactly as in the spec.
- No password in argv, `config.toml`, logs, `/status`, the `replica` block or fixtures. SQL that carries a password goes to the mysql client on stdin (`dump.Exec`).
- Admin is needed for `replica local | setup | resync | promote | remove`. Over a manager link they answer `controller_admin_required`. `replica status` is open to any user.
- Pure Go and `CGO_ENABLED=0`; no new dependency. Logging uses `slog` with `event`, and the new events are exactly those in spec section 6.
- Seed files are never backups: no manifest, and they never appear in `Held`, `Backups`, retention or restore.
- Every task updates `docs/protocol.md` when it adds a wire field or route.

## Review Focus

1. **MySQL restarts on the replica** (XAMPP Stop/Start). Expected: within one watcher tick, `replicate_do_db` is applied again before `START SLAVE`, and no change from a database outside the list lands on the replica (Task 4 test `TestWatcherRestartsAfterMySQLRestart` checks the order of the SQL statements).
2. **The node agent restarts in the middle of a load.** Expected: at start the state is `failed` with "seed incomplete", the health has a problem item that asks for resync, and the old seed file is gone (Task 5 `TestRecoverMarksIncompleteSeed`).
3. **A password leaks into argv or a log** through an error message from the mysql client (for example `ERROR 1045 … using password`). Expected: every error goes through `Conn.scrub`, and the repl password never appears in the fake's argv log or in the agent log (Task 1 `TestExecKeepsSQLOffArgv`, Task 8 `TestSetupNeverLogsThePassword`).
4. **Setup asked while a backup dump of the same database is running, or the database list is empty.** Expected: the seed dump runs next to backups (both are read-only with `--single-transaction`). An empty list refuses with `Sevjala backs up no database yet: add one with 'sevjala db set --databases …' first` (Task 8 table row).
5. **The replica's address changes (DHCP).** Expected: an IO error 1045 shows the problem item, and `replica resync` drops `sevjala_repl@<old>`, creates `@<new>` and moves the firewall rule (Task 8 `TestResyncRebindsUserToNewAddress`).

---

### Task 1: `dump` — exported queries, stdin exec, whole-server load, seed dump

**Files:**
- Modify: `dump/detect.go` (export `Query`, add `QueryNamed`, `Exec`), `dump/restore.go:84` (`Load` with `db == ""` loads without a default database).
- Create: `dump/seed.go`.
- Modify: `dump/dumptest/fake.go` (`Answers`, `STDIN` log lines, seed dump output with a GTID line).
- Test: `dump/seed_test.go`, `dump/dump_test.go`.

**Interfaces — Produces:**
- `dump.Query(ctx, tools Tools, conn Conn, sql string) ([][]string, error)`: the renamed `query`. Every caller is updated.
- `dump.QueryNamed(ctx, tools, conn, sql string) ([]map[string]string, error)`: `--batch` with column names; values unescape `\t`, `\n`, `\\` and `NULL` → `""`.
- `dump.Exec(ctx, tools, conn, sql string) error`: runs `mysql <conn.args()> --batch` with the SQL on stdin; the error is `explain`ed and scrubbed.
- `dump.Load(ctx, tools, conn, db string, gz io.Reader) error`: when `db == ""`, no database argument is passed.
- `dump.SeedOptions{Tools Tools; Conn Conn; Databases []string; LockAll bool; Path string; Progress func(int64)}`.
- `dump.SeedResult{Size int64; SHA256 string; GTID string; Flags []string}`.
- `dump.Seed(ctx, o SeedOptions) (SeedResult, error)`: writes `o.Path+".part"`, then renames it to `o.Path`. Flags: `--single-transaction` (or `--lock-all-tables`), `--master-data=2`, `--gtid`, `--databases`, `--add-drop-database`, `--routines`, `--triggers`, `--events`, `--hex-blob`, `--default-character-set=utf8mb4`, then the database names. Failure removes the `.part`. A dump without a GTID line is an error: `the dump carries no GTID position; is the binary log on?`.
- `dump.GTIDFrom(head []byte) (string, bool)`: matches `^-- SET GLOBAL gtid_slave_pos='([0-9,-]*)';` in the first 64 KiB of uncompressed output.
- `dumptest.Answers(t testing.TB, m map[string]string)`: when the SQL contains a key, the fake client prints that key's value as given (header line included). Exec calls are logged as `STDIN <sql>`.

- [ ] **Step 1: Write the failing tests**
  - `TestGTIDFrom`, a table:
    - `"-- MariaDB dump\n-- SET GLOBAL gtid_slave_pos='0-1-42';\n"` → `"0-1-42", true`;
    - multi-domain `'0-1-42,1-2-7'` → that string;
    - empty `''` → `"", true`;
    - no line → `false`.
  - `TestSeedWritesOneFileWithGTID` (fake mode `OK`): the file exists at `Path`, there is no `.part`, `SHA256` matches the file, `GTID == "0-1-42"`, and the logged argv contains `--master-data=2 --gtid --databases shop stock` and no `--no-create-db`.
  - `TestSeedLockAll`: `LockAll: true` → argv has `--lock-all-tables` and no `--single-transaction`.
  - `TestSeedWithoutGTIDFails`: a fake dump without the line → error text as above, and no file is left.
  - `TestExecKeepsSQLOffArgv`: `Exec(…, "GRANT … IDENTIFIED BY 'pw-secret'")` → the log line has `STDIN GRANT`, and the argv part contains no `pw-secret`.
  - `TestQueryNamed`: an answer `"Slave_IO_Running\tLast_IO_Error\nYes\tline1\\nline2\n"` → `[{Slave_IO_Running: Yes, Last_IO_Error: "line1\nline2"}]`.
  - `TestLoadAllDatabases`: `Load(…, "", gz)` → the fake log has `LOAD * <bytes>` (the fake prints `*` when the last argument starts with `--`).
- [ ] **Step 2: Run** `go test ./dump/` → FAIL (undefined: Seed, GTIDFrom, Exec, QueryNamed, dumptest.Answers).
- [ ] **Step 3: Implement** the functions above. The GTID scan wraps the uncompressed stdout in a writer that keeps the first 64 KiB before gzip. The fake dumper prints `-- SET GLOBAL gtid_slave_pos='0-1-42';` when the args contain `--gtid`, unless the mode is `NoGTID` (a new mode).
- [ ] **Step 4: Run** `go test ./dump/ ./coord/` → PASS (coord uses `query` through restore helpers; make sure the renames compile).
- [ ] **Step 5: Commit** `feat(dump): seed dump with GTID position, stdin exec, named queries`

### Task 2: Config sections and server-side `my.ini` helpers

**Files:**
- Modify: `config/config.go`.
- Create: `replica/myini.go`, `replica/serverid.go`, `replica/version.go`.
- Test: `config/config_test.go`, `replica/myini_test.go`, `replica/serverid_test.go`, `replica/version_test.go`.

**Interfaces — Produces:**
- `config.ControllerReplica{Agent, Host, User, PasswordRef, MyINI string; Databases []string; State string}` (toml `agent`, `host`, `user`, `password_ref`, `my_ini`, `databases`, `state`) as `Controller.Replica` (toml `replica`).
- `config.NodeReplica{Host string; Port int; User, PasswordRef, MySQLBin, MyINI string; Docker bool; ServerID int64; Databases []string; State string; PromotedAt time.Time}` as `Node.Replica`. `Validate` checks that the port is 1-65535 when `Host != ""`.
- `config.SecretReplicaRepl = "replica-repl"`, `config.SecretReplicaDB = "replica-db"`.
- `replica.Setting{Key, Value string; Flag bool}`: a `Flag` writes a bare key.
- `replica.EditMyINI(src []byte, want, disable []replica.Setting) (out []byte, changed []string)`:
  - It is pure. Keys match with `-` and `_` treated as the same and case-insensitively, in the first `[mysqld]` section. When that section is missing, it is appended.
  - An active line with a different value is replaced in place. A commented `#key=` line stays, and the new line goes after it.
  - `disable` comments out an active line whose value matches (an empty Value matches any value).
  - The CRLF or LF line endings of `src` are kept.
- `replica.ApplyMyINI(path string, want, disable []Setting) (backup string, changed []string, err error)`:
  - Copies the file to `path+".sevjala-bak"` once (`O_EXCL`; an existing backup is kept), then writes atomically.
  - With nothing changed, it does not write and does not create the backup.
- `replica.FindMyINI(explicit, binDir string, exists func(string) bool) (string, error)`:
  - Candidates: `explicit`, `binDir/my.ini`, `binDir/../my.ini`, `binDir/../etc/my.cnf`.
  - Error: `cannot find my.ini next to <binDir>; give its path with --my-ini`.
- `replica.ServerID(agentID string) int64` (formula in Global Constraints).
- `replica.SameOrNewer(replica, primary string) bool`: compares the leading `major.minor` of `VERSION()` strings.

- [ ] **Step 1: Write the failing tests**
  - `TestEditMyINI`, a table:
    - XAMPP-style file with `[mysqld]` and `# log-bin=mysql-bin` → the new line goes after the comment, and `changed = ["log-bin=mysql-bin"]`;
    - an existing `binlog_format = MIXED` → replaced with `binlog_format=ROW`;
    - an existing `log_bin=mysql-bin` (underscore) → unchanged, and `changed` is empty;
    - `bind-address="127.0.0.1"` active → becomes `#bind-address="127.0.0.1"  # turned off by Sevjala`;
    - `[client]` and `[mysqld]` both with `port` → only `[mysqld]` is touched;
    - CRLF input → CRLF output;
    - no `[mysqld]` → section appended.
  - `TestApplyMyINIBackupOnce`: two runs with different wants → the backup holds the original bytes after both.
  - `TestApplyMyININoChangeNoBackup`.
  - `TestFindMyINI`: a table over the candidates and the error text.
  - `TestServerID`: stable for one id; never 0 or 1 over 10 000 random ids; two different ids give different values (a fixed pair).
  - `TestSameOrNewer`: `("10.4.34-MariaDB-1:10.4.34+maria~ubu2004", "10.1.38-MariaDB")` → true; `("10.1.38-MariaDB", "10.4.34-MariaDB")` → false; `("10.4.1-MariaDB", "10.4.30-MariaDB")` → true (major.minor only).
  - `TestConfigReplicaRoundTrip`: write, then load, gives equal sections; `password_ref` is a ref, never a value.
- [ ] **Step 2: Run** `go test ./config/ ./replica/` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(replica): my.ini editor, server id and config sections`

### Task 3: Wire types, status parsing and the firewall rule

**Files:**
- Modify: `lanapi/types.go`, `localapi/api.go` (types only).
- Create: `replica/slave.go`, `replica/firewall.go`.
- Test: `replica/slave_test.go`, `replica/firewall_test.go`.

**Interfaces — Produces:**
- `lanapi.ReplicaInfo`: the spec's `replica` block, field for field; `BehindS *int64` and `json:"behind_s"`.
- State constants `lanapi.ReplicaPrepared|Loading|Starting|Running|Failed|Promoted`.
- `lanapi.Status.Replica *ReplicaInfo` (`json:"replica,omitempty"`).
- `lanapi.ReplicaStart{GTID string; Port int; User, Password string; Databases []string}`, with `Password` tagged `json:"password"` (sent once over mTLS and never echoed back).
- `localapi` types, exactly as named below (JSON snake_case):
  - `ReplicaLocal{Host, User, Password, MySQLBin, MyINI *string; Port *int; Docker bool}`;
  - `ReplicaLocalResult{Replica *lanapi.ReplicaInfo; ServerID int64; MyINI, Backup string; Changed []string; DockerFlags string; RestartNeeded bool; Mismatch []string}`;
  - `ReplicaSetupRequest{Agent, MyINI, Confirm string}`;
  - `ReplicaRun{Kind, Agent, Hostname, State, Step string; Bytes, Size int64; Databases []string; MyINI, Backup, Error string; StartedAt, FinishedAt time.Time}`, where `State` is `running|done|failed|restart_needed`;
  - `ReplicaView{Role, Agent, Hostname, Controller string; Databases []string; Online bool; Replica *lanapi.ReplicaInfo; Run *ReplicaRun; Items []HealthItem}`;
  - `ReplicaPromote{Force bool; Confirm string}`;
  - `ReplicaPromoted{Replica *lanapi.ReplicaInfo; Address string; Docker bool}`.
- `localapi.Health.Replica *ReplicaView` (`json:"replica,omitempty"`).
- `replica.Slave{IO, SQL string; Behind *int64; IOErrno int; IOError string; SQLErrno int; SQLError, MasterHost string}`.
- `replica.ParseSlave(rows []map[string]string) (Slave, bool)`: false when there are no rows (not a replica). It reads `Slave_IO_Running`, `Slave_SQL_Running`, `Seconds_Behind_Master` (empty → nil), `Last_IO_Errno`, `Last_IO_Error`, `Last_SQL_Errno`, `Last_SQL_Error` and `Master_Host`.
- `replica.FirewallAddCommand(port int, remoteIP string) []string`, which returns `netsh advfirewall firewall add rule name=Sevjala-Replica-MySQL dir=in action=allow protocol=TCP localport=<port> remoteip=<ip> profile=any`.
- `replica.FirewallDeleteCommand() []string`.
- `replica.OpenFirewall(port int, ip string) (bool, error)`: deletes, then adds; returns `false, nil` when not on Windows.
- `replica.CloseFirewall()`: ignores errors.

- [ ] **Step 1: Write the failing tests**
  - `TestParseSlave`, a table:
    - healthy (`Yes`, `Yes`, `0`) → `Behind == 0`;
    - IO `Connecting` with 2003 and SQL `Yes` → those values;
    - SQL `No` with 1062 and the error text → those values;
    - empty `Seconds_Behind_Master` → `Behind == nil`;
    - no rows → false.
  - `TestFirewallCommands`: exact argv for port 3306 and IP `192.168.1.21`.
- [ ] **Step 2: Run** `go test ./replica/ ./lanapi/ ./localapi/` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(replica): status block types, slave status parser, firewall rule`

### Task 4: Node — prepare (`replica local`) and the watcher

**Files:**
- Create: `replica/node.go`, `replica/watch.go`.
- Modify: `logx/logx.go`.
- Test: `replica/node_test.go`, `replica/watch_test.go`.

**Interfaces — Consumes:** Task 1 `dump.Query`/`QueryNamed`/`Exec`, Task 2 config and `EditMyINI`/`ApplyMyINI`/`FindMyINI`/`ServerID`, Task 3 types.

**Interfaces — Produces:**
- `replica.Node{AgentID, DataDir string; Ctx context.Context; Log *slog.Logger; FindTools func(dumper string) (dump.Tools, error); Load func() config.NodeReplica; Save func(config.NodeReplica) error}`:
  - `Ctx` is the agent's lifetime;
  - `Save` writes `[node.replica]` into `config.toml` under the agent's lock;
  - unexported fields: mutex, `info *lanapi.ReplicaInfo` and `ioDown time.Time`.
- `(*Node).Prepare(ctx, req localapi.ReplicaLocal) (localapi.ReplicaLocalResult, error)` follows spec section 1, steps 1–5:
  - The tools come from `req.MySQLBin`: `FindTools(filepath.Join(dir, "mysqldump"+exe))` when set, else `FindTools("")`.
  - The password is saved with `config.SetSecret(DataDir, config.SecretReplicaDB, …)` only when `req.Password != nil`.
  - It sets State `prepared` only when nothing mismatches, keeping a `running` or `promoted` state as it is.
  - It logs `replica_prepared`.
  - Errors: 409 for the MariaDB-only text and for the log-bin text.
- `(*Node).Info() *lanapi.ReplicaInfo` returns a copy, or nil when `Load().Host == ""`.
- `(*Node).Check(ctx)` does one watcher tick:
  - It fills the info from `VERSION()`, `@@server_id`, `@@read_only`, `@@log_bin`, `SHOW DATABASES` (system databases removed) and `SHOW SLAVE STATUS`.
  - It keeps `IODownSince`: set when IO leaves `Yes`, cleared on `Yes`.
  - When the state is `running`, both threads are `No`, both errnos are 0, and the node is not loading, it runs `SET GLOBAL replicate_do_db='<a,b>'` and then `START SLAVE` (via `Exec`). It logs `replica_started` with `reason=mysql restarted`.
  - When the state is `running` and a thread went from `Yes` to `No` with an error, it logs `replica_stopped` once per error.
  - A database it cannot reach sets `Error` to the scrubbed message and keeps the last thread values.
- `(*Node).Run(ctx, every time.Duration)`: runs `Check` right away and then on each tick, and stops when ctx ends.
- New events: `logx.EventReplicaPrepared, EventBinlogEnabled, EventReplicaSetupStarted, EventReplicaSeeded, EventReplicaStarted, EventReplicaStopped, EventReplicaFailed, EventReplicaResync, EventReplicaPromoted, EventReplicaRemoved`, with the string values from spec section 6.

- [ ] **Step 1: Write the failing tests** (use `dumptest.Answers` for the SQL replies)
  - `TestPrepareEditsMyINIAndAsksForRestart`: running `@@read_only=0` and a temp `my.ini` → `RestartNeeded`, `Changed` lists the four settings, the backup exists and the state stays empty.
  - `TestPrepareVerifiedIsPrepared`: running values equal the wanted ones → state `prepared`, `RestartNeeded == false`, and the event is logged.
  - `TestPrepareDockerChangesNoFile`: `Docker: true` → `DockerFlags == "--server-id=<id> --read-only=1 --skip-slave-start --relay-log=mysql-relay-bin"` and no file is touched.
  - `TestPrepareRefuses`, a table: MySQL `8.0.36` → the MariaDB-only text; `@@log_bin=1` → the log-bin text.
  - `TestPrepareNeverStoresPasswordInConfig`: after `Prepare` with a password, `Save` received `PasswordRef == "keyring:replica-db"`, and the config struct has no field holding the value.
  - `TestWatcherRestartsAfterMySQLRestart`: state `running`, slave rows `No`/`No`/0/0 → the fake log shows `SET GLOBAL replicate_do_db='shop,stock'` before `START SLAVE`.
  - `TestWatcherLeavesErroredReplicaStopped`: SQL `No` with 1062 → no `START SLAVE`, and `replica_stopped` is logged once over two ticks.
  - `TestWatcherIODownSince`: tick 1 `Connecting` at t0, tick 2 `Connecting` at t0+60 s → `IODownSince == t0`; tick 3 `Yes` → zero.
- [ ] **Step 2: Run** `go test ./replica/` → FAIL.
- [ ] **Step 3: Implement.** The watcher takes the clock from an unexported `now func() time.Time`, which tests set.
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(replica): prepare a backup PC and watch its replica`

### Task 5: Node — seed, start, stop, promote, recover

**Files:**
- Create: `replica/start.go`, `replica/promote.go`.
- Test: `replica/start_test.go`, `replica/promote_test.go`.

**Interfaces — Produces:**
- `(*Node).ReceiveSeed(sha string, size int64, body io.Reader) error`:
  - Writes `<DataDir>/replica/seed.sql.gz.part` (dir 0700) while hashing, and renames it to `seed.sql.gz` only on a size and hash match.
  - Refusals: not prepared → `ErrNotPrepared` (`this PC is not prepared as a replica: run 'sevjala replica local' on it`); state `loading`/`starting` → `ErrBusy` (`a replica load is already running here`).
  - A mismatch gives `inventory.ErrHashMismatch`-style 422 through lanapi's `receiveStatus`, so reuse inventory's error values where they fit.
- `(*Node).Start(req lanapi.ReplicaStart, masterHost string) error`:
  - Without `seed.sql.gz` it returns `ErrNoSeed`.
  - It saves State `loading` and the databases, then returns. A goroutine on `n.Ctx` then runs:
    1. STOP SLAVE; RESET SLAVE ALL;
    2. `dump.Load(…, "", file)` with byte progress into `LoadBytes`/`LoadSize`;
    3. for every row of `SELECT EVENT_SCHEMA, EVENT_NAME FROM information_schema.EVENTS WHERE EVENT_SCHEMA IN (…)`: ``ALTER EVENT `s`.`e` DISABLE ON SLAVE``;
    4. State `starting`;
    5. `SET GLOBAL gtid_slave_pos='<gtid>'`;
    6. `CHANGE MASTER TO MASTER_HOST='<masterHost>', MASTER_PORT=<port>, MASTER_USER='<user>', MASTER_PASSWORD='<pw>', MASTER_USE_GTID=slave_pos` (stdin);
    7. `SET GLOBAL replicate_do_db='<list>'`;
    8. `START SLAVE`;
    9. State `running`, and log `replica_started`.
  - Any failure sets State `failed` and `Error`, and logs `replica_failed`.
  - The seed file is deleted either way.
  - String values are SQL-escaped (`'` → `''`, `\` → `\\`).
- `(*Node).Stop(ctx) error`:
  - STOP SLAVE; RESET SLAVE ALL; `SET GLOBAL replicate_do_db=''`;
  - State `prepared`, databases cleared, and log `replica_removed`;
  - The data and `read_only` stay.
- `(*Node).Promote(ctx, req localapi.ReplicaPromote) (localapi.ReplicaPromoted, error)`:
  - Without `Confirm == "promote"` it returns `localapi.ConfirmRequired("Promoting makes this PC's MariaDB writable and stops it following <master>. Type promote to go on.", "promote", 0, nil)`.
  - With IO `Yes` and no `Force`, it returns 409 with the spec's still-sending text.
  - Then it runs STOP SLAVE; RESET SLAVE ALL; `SET GLOBAL read_only=0`; sets `read_only=0` through `ApplyMyINI` unless the replica is Docker; sets State `promoted` and `PromotedAt`; and logs `replica_promoted`.
  - `Address` is the first non-loopback IPv4 of this PC.
- `(*Node).Recover()`: called once at start. State `loading`/`starting` becomes `failed` with Error `seed incomplete: the agent stopped while loading`, and the seed files are removed.

- [ ] **Step 1: Write the failing tests**
  - `TestReceiveSeedVerifies`, a table: a good hash → the file exists; a bad hash → no file and no `.part`; not prepared → `ErrNotPrepared`; loading → `ErrBusy`.
  - `TestStartRunsTheSteps`: after Start, wait for `running`. The fake log has, in order: `STOP SLAVE`, `LOAD *`, `ALTER EVENT`, `gtid_slave_pos='0-1-42'`, `CHANGE MASTER TO MASTER_HOST='192.168.1.20'`, `replicate_do_db='shop,stock'`, `START SLAVE`. The argv part of every line lacks `pw-secret`, and the seed file is gone.
  - `TestStartLoadFailsMarksFailed` (fake `LoadFails`) → `failed`, `Error` is non-empty, the seed is gone and the event is logged.
  - `TestStartEscapesQuotes`: user `o'brien` → `MASTER_USER='o''brien'`.
  - `TestRecoverMarksIncompleteSeed`: state `loading` plus a leftover `seed.sql.gz` → after `Recover`, `failed` with the text, and no file.
  - `TestPromote`, a table:
    - no confirm → confirm_required with word `promote`;
    - IO `Yes`, no force → the 409 text;
    - IO `Connecting` → promoted, `read_only=0` in my.ini, the log has `SET GLOBAL read_only=0`;
    - Docker → no file touched, `Docker == true`.
  - `TestStopKeepsData`: the log has no `DROP DATABASE`, and the state is `prepared`.
- [ ] **Step 2: Run** `go test ./replica/` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./replica/` → PASS.
- [ ] **Step 5: Commit** `feat(replica): seed, start, stop and promote on the backup PC`

### Task 6: LAN routes and node wiring

**Files:**
- Modify: `lanapi/server.go` (`Options.Replica`, routes, the status block), `lanapi/client.go`, `svc/agent.go` (build `replica.Node` on a node, `Recover`, `Run` every 15 s, a config save under a mutex, shutdown), `coord/coord.go` (`Node.Replica *replica.Node`).
- Test: `lanapi/replica_test.go`.

**Interfaces — Produces:**
- `lanapi.Options.Replica`, which is nil on a controller:

  ```go
  interface {
      Info() *ReplicaInfo
      ReceiveSeed(sha string, size int64, body io.Reader) error
      Start(req ReplicaStart, masterHost string) error
      Stop(ctx context.Context) error
  }
  ```

- Routes behind `pairedOnly`:
  - `PUT /replica/seed`: headers `HeaderSHA256`/`HeaderSize`, body read through `deadlineReader`. Answers 200 `PutResponse`-shaped `{"sha256","size"}`, 409 for `ErrNotPrepared`/`ErrBusy`, 422 for a mismatch and 507 for a full disk.
  - `PUT /replica`: a `ReplicaStart` body (max 4 KiB). `masterHost` is the host part of `r.RemoteAddr`. Answers 202 `{"state":"loading"}`, or 409 for `ErrNoSeed`/`ErrNotPrepared`.
  - `DELETE /replica`: answers 200 `{"state":"prepared"}`.
  - Every route answers 409 `this agent is a controller` when `Replica` is nil.
- `status` sets `st.Replica = Options.Replica.Info()`.
- `(*lanapi.Client).PutReplicaSeed(ctx, addr, nodeFP, path, sha string, size int64, progress func(int64)) error`: same transport, `Expect: 100-continue` and stall watch as `PutBackup`.
- `(*lanapi.Client).StartReplica(ctx, addr, nodeFP string, req ReplicaStart) error`.
- `(*lanapi.Client).StopReplica(ctx, addr, nodeFP string) error`.

- [ ] **Step 1: Write the failing tests** (loopback, the existing paired test server helper, and a fake `Replica`)
  - `TestReplicaRoutesPairedOnly`: an unpaired certificate → 403 on all three routes.
  - `TestPutReplicaSeedStreams`: the fake receives the right sha and size.
  - `TestStartReplicaPassesRemoteHost`: the fake receives `masterHost == "127.0.0.1"` and the request.
  - `TestStatusCarriesReplica`: the fake `Info` comes back in `PollStatus`, and nil leaves out the `replica` key.
  - `TestReplicaRoutesOnController`: 409.
  - `TestStatusNeverCarriesThePassword`: after `StartReplica` with password `pw-secret`, the raw `GET /status` body does not contain `pw-secret`.
- [ ] **Step 2: Run** `go test ./lanapi/` → FAIL.
- [ ] **Step 3: Implement.** In `svc/agent.go`, the node builds `replica.Node` with `Load`/`Save` closures over `a.Config` behind a new `cfgMu`, calls `Recover()`, and starts `Run(ctx, 15*time.Second)` under a cancel that `shutdown` calls.
- [ ] **Step 4: Run** `go test ./lanapi/ ./svc/ ./coord/` → PASS.
- [ ] **Step 5: Commit** `feat(lanapi): replica seed, start and stop routes on the backup PC`

### Task 7: Health on both PCs

**Files:**
- Modify: `coord/health.go`, `svc/agent.go` (`NodeHealth` call).
- Test: `coord/health_test.go`.

**Interfaces — Produces:**
- `replicaInput{now time.Time; host, server string; info *lanapi.ReplicaInfo; offline, unfinished bool; wanted []string; onController bool}`. `unfinished`: `[controller.replica] state` is `seeding` and no setup or resync is running.
- `replicaItems(in replicaInput) []localapi.HealthItem` gives the spec's table, row for row, with the exact texts.
- `healthInput.replica *replicaInput`. `(*Controller).Health` fills it from `[controller.replica]` (when `Agent != ""`) and `pollSnapshot()`; offline means the node state is `offline` or `error`. It also sets `Health.Replica` (`ReplicaView` without `Run`).
- `NodeHealth(st *localapi.StoreInfo, minFree int64, paired bool, started, contact time.Time, rep *lanapi.ReplicaInfo, controllerHost string) localapi.Health` adds the node's own items, without the offline or databases-changed rows, and sets `Health.Replica`.

- [ ] **Step 1: Write the failing test** `TestReplicaItems`, a table with one row per spec health row plus:
  - healthy → no items;
  - 299 s behind → no item;
  - 301 s behind → the warning `is 5 min behind`;
  - IO `Connecting` for 119 s → none;
  - IO `Connecting` for 121 s → the warning with the time;
  - 1236 with IO `No` → the resync problem text;
  - `wanted` `[shop, stock, new]` against `[shop, stock]` → the databases-changed warning (controller only);
  - node offline → the offline warning, and the node's own errors are not repeated from a stale poll;
  - `unfinished` → the "did not finish" problem text.

  Also `TestControllerHealthIncludesReplica` (headline becomes the replica problem when it is the worst item).
- [ ] **Step 2: Run** `go test ./coord/ -run 'Replica|Health'` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./coord/ ./svc/` → PASS.
- [ ] **Step 5: Commit** `feat(coord): replica health on the controller and the backup PC`

### Task 8: Controller — setup, resync, remove

**Files:**
- Create: `coord/replica.go`.
- Modify: `coord/controller.go` (`RemoveAgent` calls the remove half first when the agent is the replica; fields `OpenFirewall func(port int, ip string) (bool, error)` and `CloseFirewall func()`, set by `svc` to the `replica` functions), `svc/agent.go` (at start, `[controller.replica] state == "seeding"` logs `replica_failed` with `reason=the controller stopped during setup` and is left as it is; Task 7's `unfinished` item shows it until a resync).
- Test: `coord/replica_test.go`.

**Interfaces — Consumes:** Tasks 1–7.

**Interfaces — Produces:**
- `(*Controller).Replica(ctx) (localapi.ReplicaView, error)`.
- `(*Controller).SetupReplica(ctx, req localapi.ReplicaSetupRequest) (localapi.ReplicaRun, error)`:
  - Runs spec section 2 in a goroutine on `c.Ctx` and returns the first `ReplicaRun` (State `running`, Step `checking`) after the synchronous checks pass.
  - The check errors are synchronous 409s with the spec's texts.
  - The binary-log step ends the run with State `restart_needed` plus `MyINI`/`Backup`.
  - `[controller.replica]` is saved with State `seeding` before step 3 and `running` at the end, so an interrupted run stays visible. A setup for the PC that is already the replica runs as a resync.
  - The non-InnoDB case returns `localapi.ConfirmRequired(<spec text>, "pause", 0, nil)` unless `req.Confirm == "pause"`.
- `(*Controller).ResyncReplica(ctx, confirm string) (localapi.ReplicaRun, error)`: the same run with Kind `resync`. It skips the binary-log edit (it only checks the log is on), and rebinds the user and the firewall rule when the node's `LastIP` differs from `[controller.replica] host`.
- `(*Controller).ReplicaProgress(ctx) (localapi.ReplicaRun, error)`.
- `(*Controller).RemoveReplica(ctx, force bool) (localapi.ReplicaView, error)`: with the node offline and `force == false`, it answers 409 `BACKUP-PC-1 is offline; run again with --force to remove the server side now, then 'sevjala replica remove' on BACKUP-PC-1 when it is back`.
- The run's steps, as `Step` strings:
  - `checking`
  - `enabling the binary log`
  - `creating the replication user`
  - `opening the firewall`
  - `copying the databases`
  - `sending the copy`
  - `loading on <host>`
  - `starting`
- The progress loop calls `LAN.PollStatus` on that node every 2 s and feeds `observe`, so the normal cache stays current.

- [ ] **Step 1: Write the failing tests** (loopback: `startAgent` node with a fake `lanapi.Options.Replica`, controller with `dumptest` answers, fake firewall funcs recording calls)
  - `TestSetupChecks`, a table:
    - not paired → `pairedHint` text;
    - offline;
    - no replica block → `run 'sevjala replica local' on BACKUP-PC-1 first`;
    - MySQL server;
    - replica older;
    - same server id;
    - empty database list;
    - a same-named database in `existing`;
    - another PC already the replica.

    Each row gives a 409 with its spec text, and the fake log has no `GRANT` or `mysqldump`.
  - `TestSetupEnablesBinlogThenStops`: `@@log_bin=0` → run `restart_needed`, my.ini edited (four settings), event `binlog_enabled`, no GRANT.
  - `TestSetupHappyPath`:
    - the run reaches `done`;
    - the GRANT goes over stdin to `sevjala_repl'@'127.0.0.1'`;
    - the firewall is opened with port 3306 and `127.0.0.1`;
    - the fake node got the seed sha that matches the staging file, then a `ReplicaStart` with GTID `0-1-42`, the databases `[shop, stock]` and a 24-character password;
    - the keyring holds that password under `replica-repl`;
    - `[controller.replica]` is saved with State `running`;
    - the staging seed is deleted;
    - events `replica_setup_started`, `replica_seeded` and `replica_started` are logged.
  - `TestSetupNeedsPauseForMyISAM`: a database with MyISAM → confirm_required `pause`; with confirm → `--lock-all-tables`.
  - `TestSetupNeverLogsThePassword`: scan the agent log buffer and the fake argv log for the generated password → absent.
  - `TestResyncRebindsUserToNewAddress`: config host `192.168.1.30`, node `LastIP` `127.0.0.1` → `DROP USER 'sevjala_repl'@'192.168.1.30'` and a GRANT to the new IP, the firewall reopened with the new IP, and the config host updated.
  - `TestRemoveReplica`, a table:
    - online → node Stop called, DROP USER, `CloseFirewall` called, secret deleted, section cleared;
    - offline without force → 409 text;
    - offline with force → the controller half only.
  - `TestRemoveAgentRemovesReplicaFirst`.
- [ ] **Step 2: Run** `go test ./coord/ -run Replica` → FAIL.
- [ ] **Step 3: Implement.** The password comes from `crypto/rand` as 24 characters of `[A-Za-z0-9]`.
- [ ] **Step 4: Run** `go test -race ./coord/` → PASS.
- [ ] **Step 5: Commit** `feat(coord): replica setup, resync and remove on the controller`

### Task 9: Local API, CLI and doctor

**Files:**
- Modify: `localapi/api.go` (Backend), `localapi/server.go` (routes), `localapi/client.go`, `coord/coord.go` (Service gates).
- Create: `cmd/replica.go`.
- Modify: `cmd/root.go` (register; `--controller` allowed for `replica status` only), `cmd/status.go` (the replica line), `cmd/doctor.go` (`replicaCheck`).
- Test: `localapi/localapi_test.go`, `coord/admin_test.go`, `cmd/replica_test.go`, `cmd/status_test.go`, `cmd/doctor_test.go`.

**Interfaces — Produces:**
- Backend methods:
  - `Replica(ctx) (ReplicaView, error)`
  - `PrepareReplica(ctx, ReplicaLocal) (ReplicaLocalResult, error)`
  - `SetupReplica(ctx, ReplicaSetupRequest) (ReplicaRun, error)`
  - `ResyncReplica(ctx, confirm string) (ReplicaRun, error)`
  - `ReplicaProgress(ctx) (ReplicaRun, error)`
  - `PromoteReplica(ctx, ReplicaPromote) (ReplicaPromoted, error)`
  - `RemoveReplica(ctx, force bool) (ReplicaView, error)`
- Routes as in spec section 5, with `GET /replica/run` mapped to `ReplicaProgress`.
- Client methods carry the same names.
- Every Backend fake gets the methods.
- `Service` checks the role first (`errNotNode`/`errNotController`, with the texts `run 'sevjala replica local' on the backup PC` and `run this on the controller` where they fit). It then requires `localapi.CallerIsAdmin(ctx)` for all but `Replica` and `ReplicaProgress`, else `localapi.AdminRequired(ctx, "setting up replication")` (`"promoting the replica"`, `"removing replication"`).
- `cmd`:
  - `newReplicaCmd(g)` with `local`, `setup <agent>`, `status`, `resync`, `promote [--force]` and `remove [--force]`.
  - `local` flags: `--host --port --user --password --no-password --mysql-bin --my-ini --docker`. `--password` prompts and never takes the value as an argument.
  - Setup and resync follow `ReplicaProgress` every 1 s like `watchRestore`, printing the step lines and the spec's final texts. `restart_needed` prints the restart text and exits 0.
  - Confirm words go through `withConfirm`; admin re-runs go through the existing `needsAdmin` path.
- `cmd/status.go` `renderOverview` adds `replica   BACKUP-PC-1 running, 0 s behind`. On a node it prints `replica   of SERVER-01, running, 0 s behind`, or the first replica item's text when there is one.
- `replicaCheck(v localapi.ReplicaView, err error) checkResult`:
  - OK `no replica`;
  - OK `BACKUP-PC-1 running, 0 s behind`;
  - WARN with the worst item;
  - FAIL when the read failed.

- [ ] **Step 1: Write the failing tests**
  - `TestReplicaRoutes` (localapi): every route reaches the fake with its body.
  - A row per admin action in `TestAdminOnlyWhereNecessary`: a normal user gets `admin_required`; a request through a manager gets `controller_admin_required`; `Replica` and `ReplicaProgress` stay open.
  - `TestReplicaSetupFollows` (cmd): a fake client with runs `checking` → `copying the databases` → `done` → output ends with the spec's two done lines.
  - `TestReplicaSetupRestartNeeded`: the output is the restart text, and the exit code is 0.
  - `TestReplicaLocalOutput`: `RestartNeeded` → the "Changed …" block; prepared → the "ready to be a replica" lines; Docker → the flags line.
  - `TestReplicaControllerFlag`: `--controller replica status` is allowed; `--controller replica setup x` is a usage error naming the list.
  - `TestRenderOverviewReplica`, both roles.
  - `TestReplicaCheck`, a table.
- [ ] **Step 2: Run** `go test ./localapi/ ./coord/ ./cmd/` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(cmd): sevjala replica local, setup, status, resync, promote, remove`

### Task 10: Dashboard card and tray line

**Files:**
- Create: `ui/src/Replica.svelte`.
- Modify: `ui/src/Dashboard.svelte`, `ui/src/lib/format.js` (`replicaLine`), `tray/model.go`.
- Test: `ui/src/lib/format.test.js`, `tray/model_test.go`.

**Interfaces — Consumes:** `Health.Replica` (Task 7).

**Interfaces — Produces:**
- `replicaLine(view, now) → string`, in `format.js`, for example `"BACKUP-PC-1 · running · 0 s behind"`; `"SERVER-01 → this PC · running · 0 s behind"` on a node.
- The card is shown when `health.replica` is set. It holds:
  - the line, coloured by the worst replica item;
  - IO and SQL;
  - the last error;
  - the replicated databases;
  - the command to run for each item, as text (no buttons: these are admin actions);
  - the fixed line `A replica is not a backup: a mistaken DELETE reaches it within a second.`
- `tray.Build` appends `Replica: <line>` to `Lines` when `st.Health.Replica != nil`.

- [ ] **Step 1: Write the failing tests**
  - `replicaLine`, a table: running, 7 min behind, stopped, promoted, a node view.
  - `TestBuildReplicaLine` (tray).
- [ ] **Step 2: Run** `cd ui && npm test` and `go test ./tray/` → FAIL.
- [ ] **Step 3: Implement** the card in the existing card style (Dashboard's `<article class="card">`).
- [ ] **Step 4: Run** both → PASS. Then `make build` and check in a real browser against a local controller with a fake replica block (Playwright MCP): the card at phone width and in dark mode.
- [ ] **Step 5: Commit** `feat(ui): replication card on the dashboard, replica line in the tray`

### Task 11: Two-MariaDB integration test

**Files:**
- Create: `replica/integration_test.go` (`//go:build integration`).
- Modify: `scripts/test-integration.sh`.

**Interfaces — Consumes:** `dump.Seed`, `replica.Node` (Prepare, Start, Check, Stop, Promote).

- [ ] **Step 1: Extend the script.**
  - Create a docker network `sevjala-it-$$`.
  - Primary: `mariadb:10.4 --log-bin=mysql-bin --binlog-format=ROW --server-id=1`.
  - Replica: `mariadb:10.4 --server-id=2 --read-only=1 --skip-slave-start --relay-log=mysql-relay-bin`.
  - Wait for both.
  - Build `./replica` and `./dump` tests and copy them into the primary.
  - Run the replica test with `SEVJALA_TEST_DB=127.0.0.1:3306 SEVJALA_TEST_REPLICA=<replica name>:3306 SEVJALA_TEST_PRIMARY_HOST=<primary name>`.
  - Remove both containers and the network on exit.
- [ ] **Step 2: Write `TestIntegrationReplication`.**
  1. On the primary, create `shop` (InnoDB table `t(id INT PRIMARY KEY, v TEXT)` with 1 000 rows), `other`, and `sevjala_repl@'%'` with REPLICATION SLAVE.
  2. On the replica, `SET GLOBAL server_id=<replica.ServerID(test agent id)>` (the variable is dynamic). Then `Node.Prepare` with `Docker: true` → state `prepared`.
  3. `dump.Seed` of `[shop]` into a temp file, then `ReceiveSeed` and `Start` with the primary host; wait ≤ 30 s for `running`.
  4. Insert row 1001 on the primary; the replica has it within 10 s.
  5. Insert into `other.x` on the primary; after 3 s the replica has no `other` database.
  6. On the replica, run `STOP SLAVE; SET GLOBAL replicate_do_db=''`, then call `Check`; the state is running with the filter `shop` again (query `@@replicate_do_db`).
  7. Insert id 2000 directly on the replica as root, then the same id on the primary; `Check` within 10 s shows SQL `No` with errno 1062.
  8. `Promote` with `Force` and the confirm; `@@read_only` is 0 and `SHOW SLAVE STATUS` is empty.
- [ ] **Step 3: Run** `make test-integration` → PASS (Docker is needed; record the transcript for the PR).
- [ ] **Step 4: Commit** `test(replica): primary and replica MariaDB containers end to end`

### Task 12: Records

**Files:**
- Modify: `docs/protocol.md`, `docs/decisions.md`, `docs/smoke-tests.md`, `README.md`, `docs/PROGRESS.md`.

- [ ] **Step 1:** `protocol.md`:
  - the `replica` block in `GET /status`;
  - `PUT /replica/seed`, `PUT /replica` and `DELETE /replica` (LAN);
  - the local API rows;
  - `[controller.replica]`/`[node.replica]`;
  - the seed files;
  - the `replica-repl`/`replica-db` secrets.
- [ ] **Step 2:** `decisions.md` from the next free D number (D-106 at the time of writing), one row per spec "Calls I made" item that the owner did not overrule. Each row gives the date 2026-10-18 and the reason from the spec.
- [ ] **Step 3:** `smoke-tests.md`:
  - a "Replication" section on the two real PCs: `replica local` on the backup PC (XAMPP and Docker), `replica setup`, the insert through the testbed app showing on the replica, MySQL restart on the replica, `replica resync` after a deliberate 1062, `promote`, `remove`;
  - the Testbed section's manual replication steps are replaced by a pointer to this section; the 500 MB data and app steps stay;
  - the Windows 7 and second-node items are added to "Still to do".
- [ ] **Step 4:** a README paragraph "Replication": what it is, that it is not a backup, and the two commands. Set the PROGRESS row to "code done — smokes pending".
- [ ] **Step 5:** Verify order: `go vet ./...`, `golangci-lint run`, `go test -race ./...`, `cd ui && npm test`, `make test-integration`, `make build-all`. All green.
- [ ] **Step 6: Commit** `docs: replication protocol, decisions and smoke tests`

---

## Self-review

- **Spec coverage:**

  | Spec part | Task |
  |---|---|
  | Section 1 | Task 4 |
  | Section 2 | Task 8 (with Task 1 seed dump and Task 6 routes) |
  | Section 3 | Tasks 4 and 7 |
  | Section 4 commands | Tasks 5, 8 and 9 |
  | Section 5 wire | Tasks 3, 6 and 9 |
  | Section 6 records | Tasks 2, 4 and 12 |
  | Edge-case table | Tasks 4, 5 and 8 tests |
  | "Not a backup" line | Tasks 9 and 10 |
  | Doctor (blueprint definition of done) | Task 9 |

- **Types:** `ReplicaInfo`, `ReplicaStart` (lanapi) and the `Replica*` localapi types are defined in Task 3, and later tasks use those names. Node methods are defined in Tasks 4–5 and used by Task 6's interface with the same signatures. Backend method names in Task 9 match the controller methods in Task 8 (`SetupReplica`, `ResyncReplica`, `ReplicaProgress`, `RemoveReplica`) and the node methods (`Prepare` → `PrepareReplica`, `Promote` → `PromoteReplica`).
- **Review Focus:** each of the five items names its test and the task that owns it.
- **Proportion:** the plan is about as long as the spec. It carries signatures, test names and assertions, and no code bodies.
