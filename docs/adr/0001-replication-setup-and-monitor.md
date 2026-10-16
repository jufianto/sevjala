# 0001 Replication: Sevjala sets up and watches MariaDB's own replication

**Status:** Proposed, deferred to Phase 3 (after M7). Owner decision 2026-10-17: keep the
blueprint's order; come back to this after the browser UI.
**Date:** 2026-10-17

## Context

The owner wants every write on the server (INSERT, UPDATE, DELETE) copied to a second PC
within seconds, not only at the next backup. MySQL/MariaDB do this themselves: the primary
writes each change to its binary log, and a replica reads that log over the LAN (port 3306)
and repeats it, usually less than a second behind. XAMPP's MariaDB 10.1 supports it,
including GTID. The blueprint calls Sevjala a "backup and replication manager" and keeps one
principle: Sevjala never implements replication itself (Product), and AD-17 puts
replication monitoring in Phase 3.

## Options

- **A. Copy database on the server:** reload the latest backup into `<db>_copy` on a
  schedule. Data as old as the last backup. Fits today's rules (an automatic
  `restore --into`).
- **B. Standby from backups:** a backup PC with its own MySQL keeps a loaded copy, reloaded
  after backups. Data as old as the last backup (≤10 min with `frequent`). A backup PC needs
  MySQL access.
- **C. Native replication, set up and watched by Sevjala** (this ADR): data seconds behind.
- **D. Sevjala copies changes itself** (binlog parsing, triggers): rejected, it breaks the
  "never implements replication" principle and would be far less reliable than MariaDB's.

## Decision (proposed)

C, one direction only (primary → read-only replica), MariaDB doing the copying. Sevjala:

1. **Checks** both PCs: versions compatible, port 3306 reachable, disk room for the
   binary log.
2. **Prepares the primary:** `server-id`, `log-bin`, `binlog_format=ROW` and a log expiry
   (e.g. 7 days) in `my.ini`, keeping a copy of the old file. The operator restarts MySQL
   once.
3. **Seeds the replica:** a dump that records its binary-log position, sent and loaded with
   the M4 restore path.
4. **Starts it:** `CHANGE MASTER TO … MASTER_USE_GTID`, `read_only=1`.
5. **Watches it:** `SHOW SLAVE STATUS` gives running or stopped, seconds behind and the
   last error. These show in `sevjala status`, the tray and the UI.
6. **Commands:** `sevjala replica setup | status | resync | promote`. `promote` stays manual,
   because automatic failover risks two writable PCs splitting the data.

B or A could ship first as a cheaper step, since they reuse M4 unchanged.

## Consequences

- Replication is not a backup: a mistaken `DELETE` or `DROP TABLE` reaches the replica within a
  second. Backups, retention and restore stay required, and the docs must say so plainly.
- New exceptions need new AD rows when this is accepted:
  - a node's MySQL gets credentials;
  - MariaDB stores the replication password in `master.info` on the replica, outside the
    OS keyring;
  - replication traffic runs MySQL to MySQL, outside Sevjala's mTLS, unless MariaDB TLS
    is configured as well.
- One MySQL restart on the server PC. Old XAMPP `my.ini` files vary, so the edit must be
  checked before and after, and be reversible.
- If the replica is off longer than the log expiry, it cannot catch up; `resync` re-seeds
  it from a fresh dump.
- Two-way replication (both PCs writable) is out of scope.
