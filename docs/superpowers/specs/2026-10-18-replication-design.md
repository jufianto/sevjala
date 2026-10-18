# Replication: Sevjala sets up and watches MariaDB's own replication

Owner request 2026-10-17 ([ADR 0001](../../adr/0001-replication-setup-and-monitor.md), option C);
design chosen in chat 2026-10-18 through two rounds of questions and approved ("ok its good, lets
write into plan"). New architecture decisions AD-22..AD-24 (below), accepted with the design.
The owner tried the manual steps first on two real PCs (smoke tests, Testbed section).

## Intent

A shop's MySQL PC can die at any moment. Backups bring the data back as it was at the last dump;
the owner also wants a second PC that holds every change within seconds, ready to take over by
hand. MariaDB already does that (binary log on the server, a read-only replica that repeats it).
What an office without a database admin cannot do is set it up, notice when it stops, and repair
it. Sevjala does those three things; MariaDB does the copying.

Goals:

- One command on the backup PC and one on the controller turn a paired backup PC into a
  read-only replica of the controller's databases, seeded and started, seconds behind.
- `sevjala status`, the tray and the dashboard on both PCs say whether the replica is running,
  how far behind it is and, when it stops, what to run.
- Stopped for good (log purged, address changed, broken seed): one command, `replica resync`,
  repairs it.
- Promotion of the replica to a writable server is one guided command on the backup PC, never
  automatic.

Non-goals (v1): MySQL (only MariaDB on both sides); more than one replica per controller;
two-way replication; automatic failover or switching the application over; replication of
databases Sevjala does not back up; TLS on the MariaDB replication connection; PITR.

Anti-goals (a failure even if it works):

- Sevjala's agent on a backup PC opening a LAN connection (MariaDB's replica connection is the
  one exception, AD-24).
- Any password on a command line, in `config.toml`, in a log, in `/status` or in a test fixture.
- A replica that silently stops while every screen stays green.
- Treating the replica as a backup: retention, restore and the Backups screen never show it,
  and every replica screen keeps the line "A replica is not a backup: a mistaken DELETE reaches
  it within a second."
- Sevjala deleting a database on the backup PC that it did not create there.

## New architecture decisions

| ID | Decision | Why |
|---|---|---|
| AD-22 | Accepted 2026-10-18 ([ADR 0001](../../adr/0001-replication-setup-and-monitor.md)), amends AD-17: Sevjala sets up and watches MariaDB's own replication, one direction, the controller's MariaDB as primary and one read-only replica on one paired backup PC. Sevjala enables the binary log in the server's `my.ini` (the operator restarts MySQL), creates the replication user, seeds the replica with one consistent dump of all of Sevjala's databases (sent over the LAN API, sha256-checked before it is loaded, as AD-19), starts it with GTID and watches it every poll. Promotion is manual. Sevjala itself copies no change. | Changes reach a second PC within seconds without Sevjala implementing replication; the office gets setup, alarms and repair without a database admin |
| AD-23 | Accepted 2026-10-18: the replica PC's agent keeps a login for its own MySQL in its OS keyring (`[node.replica]`, `keyring:replica-db`). The replication password is random, kept in the controller's keyring (`keyring:replica-repl`), sent once to the node over mutual TLS, and stored by MariaDB in its own `master.info` on the replica, outside any keyring. | A node needs database access to load the seed and read its replica state; MariaDB has no other place for the password |
| AD-24 | Accepted 2026-10-18, amends AD-02 and AD-04 for MariaDB's own traffic only: the replica's MariaDB connects to the server's MariaDB on its MySQL port (3306), outside Sevjala's mTLS. Sevjala limits it with a Windows firewall rule that admits only the replica PC's address and a replication user bound to that address. Sevjala's agent on the node still never dials. | MariaDB replication needs that connection; MariaDB TLS would need certificates the office cannot manage, and the LAN is the trust boundary already |

## How it works

Names: SERVER-01 is the controller (the database PC), BACKUP-PC-1 the backup PC that becomes the
replica, `shop` a database in Sevjala's list, 192.168.1.21 the backup PC's address.

### 1. Prepare the backup PC (`sevjala replica local`, on BACKUP-PC-1, admin)

The backup PC needs MariaDB of the same or a newer version than the server, running (XAMPP, a
service, or Docker).

```
sevjala replica local [--host 127.0.0.1] [--port 3306] [--user root] [--password | --no-password]
                      [--mysql-bin DIR] [--my-ini PATH | --docker]
```

1. Saves the login in `[node.replica]` (password in the keyring as `replica-db`; `--password`
   prompts, never takes the value as an argument).
2. Finds the mysql client (`--mysql-bin`, else XAMPP's bin, else PATH), connects, reads
   `VERSION()`, `@@server_id`, `@@read_only`, `@@log_bin`, and runs `SHOW SLAVE STATUS` (a
   privilege error says "use an administrator login such as root").
3. Computes this PC's server id: `1000 + (first 4 bytes of sha256(agent id) mod 1 000 000 000)`
   (never 0 or 1, the XAMPP default; the controller checks it differs from the server's).
4. Without `--docker`: finds `my.ini` (`--my-ini`, else next to the mysql binaries, else
   `../my.ini`, `../etc/my.cnf`), copies it once to `my.ini.sevjala-bak`, and sets under
   `[mysqld]`: `server-id=<id>`, `read_only=1`, `skip-slave-start`, `relay-log=mysql-relay-bin`.
   With `--docker`: changes nothing and prints the flags to add to the container command:
   `--server-id=<id> --read-only=1 --skip-slave-start --relay-log=mysql-relay-bin`.
5. Compares the running values with the wanted ones. All match → state `prepared`:

   ```
   BACKUP-PC-1 is ready to be a replica (MariaDB 10.4.34, server id 1048213337).
   Next, on the controller: sevjala replica setup BACKUP-PC-1
   ```

   Some differ (the file was just edited) →

   ```
   Changed C:\xampp\mysql\bin\my.ini (copy of the old file: my.ini.sevjala-bak):
     server-id=1048213337, read_only=1, skip-slave-start, relay-log=mysql-relay-bin
   Restart MySQL on this PC (XAMPP Control Panel: Stop, then Start), then run this command again.
   ```

Refusals: MySQL rather than MariaDB ("Replication setup supports MariaDB only; this PC runs
MySQL 8.0.36"); `@@log_bin` on ("This PC's MariaDB writes its own binary log; turn off log-bin
in its my.ini and restart MySQL, then run this again"); the PC is a controller.

### 2. Set it up (`sevjala replica setup <backup PC>`, on SERVER-01, admin)

Runs in the background like a restore; the CLI follows the steps and Ctrl-C only stops
following.

1. **Check** (nothing changed yet): the PC is a paired, online node whose last poll carries a
   replica block in state `prepared`, `failed` or `running` for resync; no other PC is the
   replica; both sides MariaDB; replica version ≥ server version (major.minor); Sevjala's
   database list is not empty; the replica's server id differs from the server's; none of the
   databases exists on the replica unless it is one this replica already holds (the node lists
   its databases in the block).
2. **Binary log**: reads `@@log_bin`, `@@binlog_format`, `@@server_id`, `@@expire_logs_days`.
   If the log is off or the format is not ROW: finds `my.ini` (`--my-ini`, else next to the
   configured dump program, else `../my.ini`), copies it once to `my.ini.sevjala-bak`, sets under
   `[mysqld]` `log-bin=mysql-bin`, `binlog_format=ROW`, `expire_logs_days=7`, `server-id=1` if
   `@@server_id` is 0, comments out an active `skip-networking` or `bind-address=127.0.0.1`
   line, logs `binlog_enabled`, and stops:

   ```
   Turned on the binary log in C:\xampp\mysql\bin\my.ini (copy of the old file: my.ini.sevjala-bak).
   Restart MySQL on SERVER-01 (XAMPP Control Panel: Stop, then Start), then run
   'sevjala replica setup BACKUP-PC-1' again. Applications lose the database for those seconds.
   ```

3. **Replication user**: random 24-character password into the keyring (`replica-repl`);
   `CREATE USER IF NOT EXISTS`/`GRANT REPLICATION SLAVE ON *.* TO 'sevjala_repl'@'<node IP>'
   IDENTIFIED BY …`, piped to the mysql client on stdin. The node IP is the address the
   controller polls it at.
4. **Firewall** (Windows only): rule `Sevjala-Replica-MySQL`, inbound TCP on the configured
   database port, `remoteip=<node IP>`. Elsewhere the step prints "Allow TCP <port> from
   192.168.1.21 in this PC's firewall if one is on."
5. **Copy**: one `mysqldump --single-transaction --master-data=2 --gtid --databases <all of
   Sevjala's databases> --add-drop-database --routines --triggers --events --hex-blob
   --default-character-set=utf8mb4` into `staging/replica-seed.sql.gz.part` (gzip, sha256, one
   pass), renamed on success. The GTID position is read from the dump's
   `-- SET GLOBAL gtid_slave_pos='…';` line. If any table is not InnoDB, `--lock-all-tables`
   replaces `--single-transaction` and the request first answers confirm_required (word
   `pause`): "shop.visits (MyISAM) cannot be copied while it changes: writes to every database
   on SERVER-01 wait until the copy is done (about 1 min for 500 MB). Type pause to go on."
6. **Send**: `PUT /replica/seed` to the node, hashed while written on the node, renamed only on
   a hash match (`replica_seeded`).
7. **Start**: `PUT /replica` with the GTID, port, user, password and the database list. The node
   answers at once and works in the background: STOP SLAVE; RESET SLAVE ALL; load the seed with
   its own login (`replica` state `loading`, bytes read); `ALTER EVENT … DISABLE ON SLAVE` for
   every event in those databases; `SET GLOBAL gtid_slave_pos`; `CHANGE MASTER TO
   MASTER_HOST=<controller IP as this request's remote address>, MASTER_PORT, MASTER_USER,
   MASTER_PASSWORD, MASTER_USE_GTID=slave_pos`; `SET GLOBAL replicate_do_db=<list>`;
   `START SLAVE`; deletes the seed. The controller polls that node every 2 s until the block
   says `running` (done) or `failed`, or 10 min pass without a byte of progress (failed).
8. Done:

   ```
   BACKUP-PC-1 is now a replica of SERVER-01 (shop, stock): 0 s behind.
   A replica is not a backup: a mistaken DELETE reaches it within a second. Backups go on as before.
   ```

The staging seed file is deleted after step 7 either way. Every SQL statement that carries a
password goes to the mysql client on stdin.

### 3. Watching

On the node, a watcher (every 15 s, and right after a start) reads `SHOW SLAVE STATUS`,
`@@read_only`, `@@server_id`, `VERSION()` and the list of databases into a cache; `GET /status`
carries it as the `replica` block, so the controller has it from its normal poll.

When the state is `running` and both threads are `No` with no error number, MySQL on the node was
restarted (`skip-slave-start`) or someone stopped it: the watcher sets `replicate_do_db` again
(MariaDB does not keep it across a restart) and runs `START SLAVE` (`replica_started`, reason
`mysql restarted`). It never starts a replica that stopped with an error.

Health items, on both PCs (the node reports its own; the controller from the poll):

| Level | When | Text |
|---|---|---|
| problem | SQL thread `No` with an error | `Replica on BACKUP-PC-1 stopped: <Last_SQL_Error>; run 'sevjala replica resync' on the controller` |
| problem | IO thread `No` (not retrying) with error 1236 | `Replica on BACKUP-PC-1 cannot continue: SERVER-01 no longer has the changes it missed; run 'sevjala replica resync' on the controller` |
| problem | IO thread `No` with any other error | `Replica on BACKUP-PC-1 stopped receiving: <Last_IO_Error>` |
| problem | state `failed` (seed incomplete, load failed) | `Replica on BACKUP-PC-1 is incomplete: <reason>; run 'sevjala replica resync' on the controller` |
| problem | `[controller.replica] state` is `seeding` and no setup or resync is running (controller only) | `Replica on BACKUP-PC-1 is not set up: the last setup did not finish; run 'sevjala replica resync' on the controller` |
| warning | `Seconds_Behind_Master` > 300 | `Replica on BACKUP-PC-1 is 7 min behind` |
| warning | IO thread not `Yes` for more than 120 s (retrying) | `Replica on BACKUP-PC-1 cannot reach SERVER-01 since 14:02: <Last_IO_Error>` |
| warning | the node is offline (controller only) | `Replica PC BACKUP-PC-1 is offline; its copy is not being updated` |
| warning | Sevjala's database list differs from the replicated list | `Databases changed since the replica was set up: run 'sevjala replica resync'` |
| warning | state `promoted` | `BACKUP-PC-1 was promoted at 09:14 and is no longer a replica; run 'sevjala replica remove' on the controller` |
| warning | `@@read_only` is 0 while `running` | `Replica on BACKUP-PC-1 accepts writes (read_only is off): changes made there are lost at the next resync` |

Healthy: no item; `sevjala status` adds `replica   BACKUP-PC-1 running, 0 s behind` (controller)
or `replica   of SERVER-01, running, 0 s behind` (node).

### 4. Other commands

- `sevjala replica status` (any user, either PC; on a manager also `--controller`): the block as
  a few lines: replica, state, IO/SQL, behind, last error, databases, since.
- `sevjala replica resync` (controller, admin): steps 1, 3 (recreated for the node's current
  address, old host entry dropped), 4, 5, 6, 7 of setup. Needed after a database list change, a
  1236 error, a changed address or a failed seed. Databases removed from Sevjala's list stay on
  the replica untouched and are no longer updated.
- `sevjala replica promote` (backup PC, admin, typed confirmation `promote`): refuses while the
  IO thread is `Yes` ("SERVER-01 is still sending changes: promoting now splits the data. If
  SERVER-01 is really gone, run with --force") unless `--force`. Then STOP SLAVE, RESET SLAVE
  ALL, `SET GLOBAL read_only=0`, `read_only=0` in `my.ini` (Docker: "remove --read-only=1 from
  the container command"), state `promoted`, `replica_promoted`, and prints:

  ```
  BACKUP-PC-1's MariaDB now accepts writes. Point your application at 192.168.1.21:3306.
  Do not use SERVER-01's database again until it is set up as a replica or restored:
  two writable copies split the data. Sevjala's roles do not change; backups still come from SERVER-01.
  ```

- `sevjala replica remove` on the controller (admin): `DELETE /replica` to the node (STOP SLAVE,
  RESET SLAVE ALL, filter cleared, state back to `prepared`; data and `read_only` stay), DROP USER,
  firewall rule removed, keyring entry deleted, `[controller.replica]` cleared, `replica_removed`.
  It leaves the binary log on and prints how to turn it off (restore `my.ini.sevjala-bak` or delete
  the `log-bin` line, restart MySQL). With the node offline it refuses unless `--force`, which
  does the controller half and says "run 'sevjala replica remove' on BACKUP-PC-1 when it is back".
- `sevjala replica remove` on the backup PC (admin): the node half alone.

### 5. Wire and API

LAN API, node only, all behind `pairedOnly`:

| Route | Body | Answer |
|---|---|---|
| `PUT /replica/seed` | gzip'd dump, headers `X-Sevjala-Sha256`, `X-Sevjala-Size`, `Expect: 100-continue` | 200 `{"sha256","size"}`; 409 not prepared / a load is running; 422 hash mismatch; 507 disk full |
| `PUT /replica` | `{"gtid","port","user","password","databases":[…]}` | 202 `{"state":"loading"}`; 409 no seed / not prepared |
| `DELETE /replica` | none | 200 `{"state":"prepared"}` |
| `GET /status` | — | gains `"replica": {…}` (absent when not prepared) |

`replica` block: `state` (`prepared`, `loading`, `starting`, `running`, `failed`, `promoted`),
`flavour`, `version`, `server_id`, `read_only`, `log_bin`, `io` (`Yes`/`No`/`Connecting`), `sql`,
`behind_s` (null when unknown), `io_errno`, `io_error`, `sql_errno`, `sql_error`, `io_down_since`,
`databases` (replicated list), `existing` (non-system databases on this MariaDB), `master_host`,
`load_bytes`, `load_size`, `error`, `checked_at`, `promoted_at`.

Local API:

| Route | Role | Admin | Notes |
|---|---|---|---|
| `GET /replica` | both | no | controller: config + last poll's block + run; node: its block |
| `PUT /replica/local` | node | yes | prepare; answers the checks and `restart_needed` |
| `POST /replica/setup` | controller | yes | `{"agent","my_ini","confirm"}` → run |
| `POST /replica/resync` | controller | yes | `{"confirm"}` → run |
| `GET /replica/run` | controller | no | progress of the last setup/resync |
| `POST /replica/promote` | node | yes | `{"force","confirm"}` |
| `DELETE /replica` | both | yes | `?force=1` on the controller |

Admin actions arriving over a manager link answer `controller_admin_required` (AD-21).

### 6. Records

- `config.toml`: `[controller.replica]` `agent`, `host` (the node IP the user is bound to),
  `user` (`sevjala_repl`), `password_ref` (`keyring:replica-repl`), `my_ini`, `databases`,
  `state` (`seeding`, `running`); `[node.replica]` `host`, `port`, `user`, `password_ref`
  (`keyring:replica-db`), `mysql_bin`, `my_ini`, `docker`, `server_id`, `databases`, `state`,
  `promoted_at`.
- Seed files: `staging/replica-seed.sql.gz[.part]` on the controller; `<data>/replica/seed.sql.gz[.part]`
  on the node. Not backups: no manifest, never listed, never in retention; the existing `.part`
  clean-up at start removes leftovers.
- Events: `replica_prepared`, `binlog_enabled`, `replica_setup_started`, `replica_seeded`,
  `replica_started`, `replica_stopped`, `replica_failed`, `replica_resync`, `replica_promoted`,
  `replica_removed`. Each carries `hostname`; none carries a password.

## Edge cases

| Case | Behaviour |
|---|---|
| A database of the same name already on the replica, not one it replicates | setup refuses: "BACKUP-PC-1's MariaDB already has a database named shop that Sevjala did not put there; rename or drop it there first" |
| Non-InnoDB tables | `--lock-all-tables`, confirm word `pause` with the write-pause warning (step 5) |
| Binary log purged before the replica caught up (error 1236) | problem item with the resync hint |
| MySQL restarted on the replica | watcher re-applies the filter and starts it (section 3) |
| MySQL restarted on the server | IO thread retries (`Connecting`); warning after 120 s; no action |
| Sevjala's database list changed | warning item; nothing is copied for the new database until resync |
| MySQL on either side | refused with the MariaDB-only text |
| Replica older than the server | "BACKUP-PC-1 runs MariaDB 10.1 and SERVER-01 10.4: a replica needs the same or a newer version" |
| Node offline or restarted while loading | node start sees state `loading`/`starting` → `failed` "seed incomplete"; problem item; resync |
| Controller restarted during setup | `[controller.replica]` (saved with state `seeding` before the replication user is made) stays `seeding`; at start the agent logs `replica_failed`; the "did not finish" problem shows until a resync. A setup for the PC that is already the replica runs as a resync |
| Promote while the server still sends | refused unless `--force` |
| Replica's server id equals the server's | setup refuses: "both MariaDB servers have server id 1; run 'sevjala replica local' on BACKUP-PC-1 again" |
| XAMPP default `server-id=1` on both | `replica local` always sets the derived id, so only a hand edit collides |
| The node's address changes (DHCP) | IO error 1045 → problem item; resync rebinds the user and the firewall rule |
| Events in the replicated databases | disabled on the replica after the load (`DISABLE ON SLAVE`) so they do not write there |
| Replica's own binary log on | `replica local` refuses (GTID position would conflict) |
| Setup asked for a second PC | "BACKUP-PC-1 is already the replica; run 'sevjala replica remove' first" |
| The node is removed (`agents remove`) while it is the replica | removal does the controller half of `replica remove` first |
| Disk too small on the node for the seed | 507 from the node, as for backups |
| The configured database user lacks RELOAD / REPLICATION CLIENT | the dump's error, explained like backup errors, plus "the database login needs RELOAD and REPLICATION CLIENT rights" |

## Left to the builder

The exact column layout of `replica status`, the dashboard card's layout (one card, same style as
the others, both PCs), log message wording other than the texts above, and how the node streams
load progress (bytes of the gzip read).

## Calls I made (owner can overrule)

1. **One multi-database seed dump** instead of the per-database backup pipeline described in chat:
   per-database dumps each have their own binary-log position, and one replica starts from one
   position.
2. **The seed is a separate artifact**, not a backup: no manifest, never in the inventory,
   retention, restore lists or the Backups screen; deleted after use.
3. **MASTER_HOST** is the controller's address as the node sees the `PUT /replica` request
   (its remote address), so it is the address that works from the node.
4. **The replication user's host** is the node's address as the controller polls it.
5. **`expire_logs_days=7`**: a replica off for more than a week needs a resync.
6. **Keyring names** `replica-repl` (controller) and `replica-db` (node); config sections
   `[controller.replica]` and `[node.replica]`; user `sevjala_repl`; firewall rule
   `Sevjala-Replica-MySQL`.
7. **Event names** as listed in section 6.
8. **IO errors**: an IO error while the thread is still retrying (`Connecting`) is a warning after
   120 s; only a stopped IO thread (`No`) with an error is a problem. A server restart would
   otherwise turn everything red for a few seconds.
9. **Restart detection** uses "running state, both threads No, no error" rather than an Uptime
   drop: it also covers the agent itself restarting, and needs no remembered uptime.
10. **Typed confirmation `pause`** before a `--lock-all-tables` seed, and **`promote`** before a
    promotion; `remove` needs none (it deletes no data).
11. **The replica's own binary log must be off** (GTID conflicts otherwise).
12. **Databases dropped from Sevjala's list** stay on the replica, no longer updated.
13. **`replica remove` runs on either PC**: the full remove on the controller, the node half on a
    backup PC (for a node that was offline during the controller's remove).
14. **Load progress**: the controller follows the node with a 2 s poll during setup and fails it
    after 10 min with no progress.
