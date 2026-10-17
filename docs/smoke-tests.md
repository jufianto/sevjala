# Manual smoke tests

Checks that CI cannot run: they need root, a service manager, or real machines (P0, M1). Paste each
transcript into the milestone's PR. Pending ones are listed in [PROGRESS.md](./PROGRESS.md).
For a first guided run on one Windows PC and one Mac, start with
[test-walkthrough.md](./test-walkthrough.md).

## Installer — double-click setup (Windows)

Covered without Windows: the wizard's steps (choices, copy, install, PATH, pairing, update,
uninstall) with fakes in `cmd/setup_test.go`. Not covered: the real double-click, UAC, PATH in
the registry, and Windows 7. A Windows VM (UTM or Parallels on the Mac; Windows 11 ARM runs the
x64 exe) covers all but Windows 7.

1. Download `sevjala-win7-amd64.exe` from the release to `Downloads` and double-click it.
   The UAC prompt appears; after "Yes" a new console shows `Sevjala setup <version>` and the
   two roles. "No" on UAC: the first window says administrator rights are needed and waits
   for Enter.
2. Choose 2 (node). Expect `copied to C:\Program Files\Sevjala\sevjala.exe`, the firewall
   line, the service status, the PATH line and a pairing code; `Press Enter to close` keeps
   the window open. `services.msc` shows "Sevjala Agent" running; `Downloads` can now be
   emptied without breaking it.
3. On the XAMPP PC, double-click and choose 1 (controller): `db detect` lists the databases;
   type one, then the MySQL password (not echoed; Enter if root has none). A wrong password
   says "not saved" and asks again. Then it discovers nodes; type the node's
   `sevjala pair …` line and see "paired with …". Enter finishes.
4. Open a **new** Administrator prompt anywhere: `sevjala status` works (PATH).
5. Double-click the same downloaded exe again: it shows the status and the menu (Update /
   Show a pairing code or Pair a node / Uninstall / Exit). Update stops the service, replaces
   the exe and starts it; `sevjala version` shows the new version.
6. Uninstall from the menu: the service and firewall rules are gone, `sevjala` is no longer on
   the PATH in a new prompt, `C:\Program Files\Sevjala\sevjala.exe` is deleted, and
   `C:\ProgramData\Sevjala` (backups, settings) is kept.
7. Typing `sevjala` with no arguments in cmd still prints the help (no wizard, no pause).
8. Windows 7 (32- and 64-bit): steps 1–6 with `sevjala-win7-386.exe` / `-amd64.exe`.

## P0 — service install and status

Exit criterion: `sevjala service install && sevjala service status` works on Windows 7,
Windows 11, macOS and Linux.

### Build the binaries (on the Mac)

```sh
make build        # ./sevjala for this machine
make build-all    # dist/: sevjala-{windows,win7}-*.exe, -darwin-*, -linux-amd64, checksums.txt
```

### macOS / Linux

```sh
sudo ./sevjala service install --role node   # prints the status block, exit 0
./sevjala service status                     # see "Expected" below; exit 0
sudo ./sevjala doctor                        # four "ok" lines, exit 0
sudo ./sevjala service stop
./sevjala service status; echo "exit=$?"     # service stopped, agent not answering, exit=1
sudo ./sevjala service start                 # status block again, exit 0
sudo ./sevjala service uninstall             # says "data kept in …"
```

- Without sudo, `status` can show `service unknown (run with sudo for the service state)`
  while the agent answers; that still exits 0. Members of the `sevjala` group reach the socket
  without sudo after logging out and back in (install adds `$SUDO_USER` to the group).
- Data dir: macOS `/Library/Application Support/Sevjala/`, Linux `/var/lib/sevjala/`.
  Log: `<data dir>/logs/sevjala.log` (JSON lines; check `service_installed`, `agent_started`,
  `agent_stopped`, `service_uninstalled`).
- Cleanup after the test: `sudo rm -r` the data dir. The `sevjala` group stays.

### Windows 7 VM (and Windows 11)

VM: Windows 7 SP1 with KB3125574, bridged network (M1 needs it), on the Windows 11 PC. Copy
`dist/sevjala-win7-amd64.exe` (and `sevjala-win7-386.exe` on a 32-bit VM; use
`sevjala-windows-amd64.exe` on Windows 11). From an **Administrator** command prompt:

```bat
sevjala-win7-amd64.exe version
sevjala-win7-amd64.exe service install --role node
sevjala-win7-amd64.exe service status
sevjala-win7-amd64.exe doctor
sevjala-win7-amd64.exe service stop
sevjala-win7-amd64.exe service status
echo exit=%ERRORLEVEL%
sevjala-win7-amd64.exe service start
sevjala-win7-amd64.exe service uninstall
```

- `status` after `stop` must exit 1; every other command exits 0.
- A non-admin prompt must refuse `install` with `run this from an Administrator prompt`
  (this is the check that was broken on Windows 7 before the fix in `svc/admin_windows.go`).
- Data dir `C:\ProgramData\Sevjala\`; the service shows as "Sevjala Agent" in `services.msc`.

### Expected status block

```
service   running      (windows-service)
agent     answering    v0.1.0  role=node  up 2m13s  pid 4120
data dir  C:\ProgramData\Sevjala
```

The platform in brackets is `darwin-launchd` on macOS and `linux-systemd` on Linux.

## M1 — discover, pair, ping across two PCs

Exit criterion: discover → pair → ping across two real PCs; an unpaired peer is rejected at TLS.

Setup: PC A (controller) and PC B (node) on the same LAN (the Win7 VM needs **bridged**
networking). A third machine C as a stranger controller is optional but proves the rejection.
Run every command from an Administrator prompt / with sudo.

```sh
# A
sevjala service install --role controller
# B
sevjala service install --role node
# A
sevjala discover                      # B listed, PAIRED no
# B
sevjala code                          # note the code, B's 8-char id and fingerprint
# A
sevjala pair <B> --code <code> --fp 0000000000000000   # "do not pair", exit 1; the code still works
sevjala pair <B> --code 000000 --fp <fp>              # wrong code → "wrong pairing code", exit 1
sevjala pair <B> --code <code> --fp <fp>              # "paired with B (…) sha256:…"
sevjala ping <B>                      # "answered in N ms: node …, healthy"
sevjala agents                        # B online, LAST SEEN now
sevjala discover                      # B listed, PAIRED yes
# C (stranger controller)
sevjala service install --role controller
sevjala agents add <B ip>
sevjala pair <B> --code 123456 --fp <B fp>   # "has no active pairing code", exit 1; B logs peer_rejected (TLS)
# B: restart the service, then on A
sevjala ping <B>                      # still answers
```

Windows checks on each Windows PC:

- `sevjala doctor` shows `ok lockdown`, `ok lan api`, `ok firewall`.
- Install once more on a Win7 PC with UAC turned off (or as the built-in Administrator): it must
  succeed and `doctor` must show `ok lockdown` (the new folder is owned by that user at first).
- `icacls C:\ProgramData\Sevjala` lists only `NT AUTHORITY\SYSTEM` and `BUILTIN\Administrators`.
- `netsh advfirewall firewall show rule name=Sevjala-LAN-API` and `…name=Sevjala-Discovery` exist;
  after `sevjala service uninstall` both are gone.

macOS/Linux: `sudo sevjala doctor` shows `ok lockdown` (`ls -ld` on the data dir: `drwx------ root`).

Logs on B (`<data dir>/logs/sevjala.log`) contain `pair_code_issued`, `pair_rejected`,
`pair_succeeded`, `peer_rejected` and never the code itself.

## M2 — first backup to three PCs

Exit criterion: a 300 MB XAMPP database dumped once and present, hash-verified, on three
nodes; killing a node agent mid-transfer leaves no partial file.

Already covered without real PCs (2026-10-13, Docker on the Mac): MariaDB 10.4 with a root
password containing spaces, quotes, `$` and `ö`; a controller and three node agents in
separate containers; 162 MiB dump → 3/3 verified with matching `sha256sum`; a frozen node
(`docker pause`) failed after the 60 s stall and verified on the retry; a controller restart
mid-transfer requeued the copy; stale `.part` files were removed at agent start. What
remains is real Windows, XAMPP and three PCs.

Setup: the XAMPP PC (controller, MySQL running) and three node PCs, all installed and paired
as in M1 (`pair` now needs `--fp`). Run every command from an Administrator prompt / sudo.

```sh
# controller
sevjala db detect                    # dumper C:\xampp\mysql\bin\mysqldump.exe, databases listed
sevjala db set --db <name>           # add --password if root has one (prompted, not echoed)
sevjala doctor                       # ok staging, ok dumper, ok database … single-transaction
sevjala backup now                   # dumping… → created → three "verified"; exit code 0
sevjala backups list --matrix        # three v (✓ on macOS/Linux)
```

On each node, the store (`C:\ProgramData\Sevjala\store`, or `<data dir>/store`) holds
`BK-….sql.gz` and `BK-….json`. `certutil -hashfile BK-….sql.gz SHA256` (macOS
`shasum -a 256`) equals the `sha256` in the `.json` and in `sevjala backups list` on the
controller.

Interruptions:

1. Start `sevjala backup now`; while a copy shows `transferring`, run `sevjala service stop`
   on that node. The copy turns `pending (…)` and, if the node stays down, `offline` after
   the 30 s / 2 min / 10 min retries. `sevjala service start` on the node: its store has no
   `.part` file, and its log shows `part_removed` if one was left.
2. During `transferring`, run `sevjala service stop` then `start` on the controller. The copy
   is sent again after the start and ends `verified` (`sevjala backups list`).
3. Ctrl-C during `backup now` prints "still running in the agent"; the backup still finishes.

Secrets:

- Windows 7: after `db set --password`, `config.toml` has `password_ref = "keyring:db"` and
  no password. As SYSTEM (`psexec -s cmd`), `cmdkey /list` shows `sevjala:db`.
- macOS (sudo install): `sudo sevjala db set --password` succeeds (System keychain as root),
  and `backup now` still works after a reboot.
- Linux without a desktop session: the agent log shows `keyring_fallback`;
  `/var/lib/sevjala/secrets/db.enc` and `key` are `-rw------- root`.
- `grep -ri '<password>' <data dir>/logs <data dir>/config.toml` finds nothing on any PC.

Logs: the controller has `backup_started`, `backup_created`, `transfer_started`,
`transfer_verified`; each node has `backup_received`.

Win7-only risks found in review (no automated coverage):

- `db set --password` with a non-ASCII password (e.g. `wörd`): it must either work, with a
  backup succeeding afterwards, or be refused at `db set` with "Access denied". It must
  never be saved broken.
- Re-pair one node to another controller, then `backup now` on the first: that copy must
  end `failed` after one attempt ("rejected this controller's certificate"), not retry for
  12 minutes.

## M3 — unattended

Exit criterion: 48 h unattended with one node powered off for 2 h; every node ends with
exactly the policy counts, nothing deleted that a node still needed, and
`sevjala retention run --dry-run` prints "nothing to do".

Already covered without real PCs: `TestUnattended` (two in-process nodes on loopback, fast
schedules) runs the schedule, takes a node offline and back, fills it in, trims it, replaces
a corrupt copy and keeps exactly the newest staging files, ending with an empty dry run.
What remains is real time, real PCs and a real power-off.

Setup: the M2 setup (controller with a database chosen, three paired nodes).

```sh
# controller
sevjala policy list                  # frequent, hourly, daily, monthly, manual, pre-restore; NEXT RUN filled in
sevjala doctor                       # ok schedule (next run …), ok nodes (3 online)
sevjala agents                 # STATE online, FREE filled in for every node
```

48 h run:

1. Leave everything running. After ~1 h, `sevjala backups list --matrix` shows `frequent`
   backups every 10 min and one `hourly`, all `v` on every node.
2. Power one node off (not just stop the service) for 2 h. Within a minute
   `sevjala agents` shows it `offline`. Backups keep being made; its column shows
   pending/offline copies. Nothing on the other nodes is deleted early.
3. Power it back on. Within 5 min its column fills in (`backfill_queued` in the controller
   log) and then trims to the policy counts (`retention_delete`).
4. After 48 h, on each node count the files per tag in the store (`*.json`, `tags`): at most
   12 frequent, 24 hourly, 2 daily; staging on the controller holds at most 12 backups per
   database. `sevjala retention run --dry-run` prints "nothing to do".
5. Reboot the controller PC once during the run: missed slots catch up with one backup, not
   one per missed slot (`schedule_fired` in the log).

Disk full: on one node set `[node.store] min_free_bytes` above its free space and restart
it. `sevjala agents` shows `warning` with "free space … is below the floor …";
`retention run --dry-run` lists its missing backups as "waiting for free space"; new copies
to it fail with 507 and are not retried in a loop. Restore the floor: within 5 min the
copies are sent.

Damaged copy: on a node, truncate the newest `BK-….sql.gz` by hand (e.g. copy the first
half over it). Within a minute `sevjala agents` shows `warning`; within 5 min the controller
logs `retention_delete` ("damaged: replaced with a good copy") and the copy is `v` again with
the right sha256. Do the same to an old backup no longer staged: it is kept and listed by
`retention run --dry-run` as "damaged, and the controller has no good copy left to replace it".

Clock moved back: set the controller PC's clock back one day (as after a dead CMOS battery).
`retention run --dry-run` keeps everything with "a backup is dated after now (clock moved
back) …"; backups still run and are sent. Set the clock right: the next run trims normally.

Policies:

```sh
sevjala policy add weekly --schedule @weekly --keep 4      # listed, NEXT RUN next Sunday 00:00
sevjala policy set frequent --keep 6 --to <node hostname>  # dry run: other nodes lose frequent copies
sevjala retention run --dry-run                            # shows deletes with reasons, changes nothing
sevjala retention run                                      # applies them
sevjala policy set frequent --keep 12 --to all
sevjala policy remove weekly                               # its backups stay
sevjala policy remove manual                               # refused
sevjala backup now --tag nope                              # refused: "tag "nope" is not a policy"
sevjala policy set daily --disable                         # ENABLED no; retention still keeps its backups
```

Logs: the controller has `schedule_fired`, `agent_online`/`agent_offline`, `backfill_queued`,
`retention_delete`, `staging_pruned`; each node has `backup_deleted` with the reason.

## M4 — restore

Exit criterion: a restored database's `CHECKSUM TABLE` equals the original's for every table.

Already covered without real PCs: the coord tests restore from the controller's own copy,
from a node (skipping a changed or truncated copy on another node and marking it corrupt),
from an exported file, and replace production (pre-restore backup first, backups refused
during the load, the way back named on failure). `make test-integration` loads a real dump
into MariaDB 10.4 and compares `CHECKSUM TABLE` per table.

Done on the owner's controller (2026-10-16, Windows, XAMPP MariaDB 10.1.38, a 45 MB database
that dumps to 10.8 MiB): `backup now`, then `restore latest --into <db>_smoke` took 8 s
(checking the controller's own copy, then loading), 21 tables, and `CHECKSUM TABLE <db>.t,
<db>_smoke.t EXTENDED` was equal for all 21. Loading is the slow direction: expect a few
minutes for a 500 MB database.

Also done there (2026-10-17, build `0.3.0-alpha.2+16`, on a scratch copy of that database added to
the backup list for the test): replace with a wrong name cancels; replace with the name typed
backs up first and finishes in 6 s, `CHECKSUM TABLE` equal 21/21; export then `--file` restore in
4 s, equal 21/21; a damaged exported file is refused before any database exists; `--status`
follows a restore started elsewhere; MySQL killed mid-load: the restore fails naming the way
back, `backup now` of that database is refused with the same command also after `service stop`
/ `start`, and the way back finishes in 8 s without a new pre-restore backup, clears the block,
equal 21/21, and backups of it work again.

With a macOS node (2026-10-17): a backup whose only copy was on the node (the controller's
own copy moved aside) was fetched from it and restored, equal 21/21; `backup now` while the
node was off ended with "missing copies are sent when those PCs answer again" instead of
waiting; `sevjala restore` asked on the node is refused with the export hint; a byte flipped in
the node's copy: the restore refuses it ("damaged"), creates no database and `backups list
--matrix` shows that copy `!`. With build `0.3.0-alpha.2+25` (D-065) the next reconciliation
deleted that copy on the node and sent the controller's good copy again (`!`, then `.`, then
`v` within 5 min); exporting it on the node passes the sha256 check.

Still to do on real PCs (controller + at least one node):

```bat
sevjala restore latest
```

**Expect:** `Restoring BK-… (<db>) into <db>_restored_<date>`, a step line per step, a
percentage at most every 5 s, then `Restored … : N tables, in …, from the controller's own
copy.` and `The live database <db> was not touched.` Compare a few tables:
`CHECKSUM TABLE <db>.<t>, <db>_restored_<date>.<t> EXTENDED`. Drop the copy when done.

1. From a node: pick a backup no longer in the controller's own copy (older than the newest
   12, `backups list` shows it `distributed`) and restore it with `--into <db>_fromnode`.
   The result says `from <node hostname>`; the node's log has `backup_served`.
2. Damaged node copy: with two nodes holding the backup, flip a byte in the newer node's
   `BK-….sql.gz`. The restore logs `restore_source_failed` and `copy_corrupt` for that node,
   finishes from the other one, and `backups list` shows that copy `corrupt` until retention
   replaces it.
3. Ctrl-C while it loads: the CLI says it is still running; `sevjala restore --status`
   follows it to the end.
4. From a file (on a second PC acting as a new controller, or the same one):
   `sevjala backups export latest --to D:\export` on a node, copy the folder over, then
   `sevjala restore --file D:\export\<db>\<date>.sql.gz --into <db>_fromfile`. Change one
   byte of the file: the restore fails with "damaged" before anything is created.
5. Replace production, **on a test database only** (configure a scratch database first, e.g.
   `db set --db scratch`): `sevjala restore latest --replace-production`. It shows the backup,
   its date and size, and asks for the name; typing anything else cancels with "nothing was
   changed". Typing the name: a pre-restore backup `BK-…` tagged `pre-restore` appears first, then
   the drop and load; the result names the pre-restore backup and the command to undo. While
   it loads, `sevjala backup now` is refused ("being replaced by a restore").
6. Failed replace, on the scratch database: stop MySQL while `--replace-production` loads. The
   restore fails naming `sevjala restore <pre-restore id> --replace-production`; `backup now`
   of it is refused with the same command, also after restarting the service
   (`<data-dir>\replace-incomplete-<db>` exists). Start MySQL and run that command: it does
   not back the broken database up again, finishes, and backups of it work again.

Caution: a restored copy keeps the triggers, views and routines as they were dumped. One
that names the original database explicitly (`shop.orders` rather than `orders`) still
points at the original from inside the copy; check before testing an application against it.

Logs: the controller has `restore_started`, `restore_succeeded` (source, duration, tables) or
`restore_failed` (step, error), and `restore_source_failed` / `copy_corrupt` per bad source.

## M5 — operator comfort

Exit criterion: a non-engineer can tell from the tray alone whether protection is healthy.

Already covered without real PCs: the coord tests compute every health rule (protected,
overdue, failed dump, half-loaded replace, disk, PC off, stuck copy, damaged copy, no schedule,
no database; node: not paired, silent controller, disk), the admin-only actions refused to a
normal caller and allowed to an administrator, and `sevjala status` against golden output.
The localapi tests carry `admin_required` and the caller identity over a real socket; the
Windows SDDL is pinned by a test. The tray builds for every asset, including both go-win7 ones.

Done on macOS (2026-10-17): `sevjala tray` ran 5 s against the Mac node's older agent without a
crash; `sevjala status` there showed WARNING and "the agent is older than this command; run:
sevjala update".

Done on the Windows controller (2026-10-17, build `0.3.0-alpha.2+38`, over an elevated SSH session):
`sevjala update --from` works; `sevjala status` shows OK with the last backup, its copies, the next
run, the backup PC and free disk. The first update from an older build runs the old updater, so
the tray's start at login (`HKLM\...\Run` value `Sevjala Tray`) appears only after a second
`sevjala update` (or `sevjala service install`); from this build on, every update adds it.
Not testable over SSH (no desktop, and the session is elevated): the tray itself and the
non-admin prompt.

Still to do on real PCs (controller + at least one node), after `sevjala update` to this build:

1. **Daily commands without admin** (Windows, ordinary Command Prompt): `sevjala status`,
   `backups list`, `backup now`, `doctor` (data-folder lines say `skip`) and `restore latest`
   all work with no UAC prompt.
2. **Admin commands ask:** `sevjala pair …` shows UAC and finishes in a second window
   (`finished in the administrator window (exit 0)`); answering No prints "Cancelled:
   administrator rights were refused; nothing was changed." The same for `service stop`,
   `restore --file`, `backups export latest --to <folder>`, `agents remove`, `backup now --tag daily`,
   `policy set frequent --keep 6` and `policy set frequent --schedule "*/15 * * * *"`.
   `policy set frequent --keep 20` and plain `backup now` do not ask.
   macOS/Linux: the same commands ask for the password through `sudo`.
3. **Tray colours** (Windows controller): green with the last backup in the tooltip; switch
   the node off → yellow within a minute ("… has not answered since …"); stop MySQL and run
   `backup now` → red ("The last backup of … failed"); `sevjala service stop` → grey ("Sevjala
   service is not running"); start everything → green again. "Back up now" in the menu starts a
   backup; "Open status" opens a window with `sevjala status`.
4. **At login:** after `setup`, the tray appears without a reboot; log out and in, it appears
   again (no lasting console window; a brief flash is accepted). macOS node: the same after
   login (`/Library/LaunchAgents/dev.jufi.sevjala.tray.plist`).
5. **Update and uninstall:** `sevjala update --from <file>` with the tray running: within 15 s
   the tray restarts on the new exe (Task Manager shows one `sevjala.exe tray`), and a later
   update does not fail on a locked `sevjala.exe.old`. `setup` → uninstall: the tray quits
   within 15 s and `C:\Program Files\Sevjala` is gone.
6. **Pipe squatting** (optional): with the service stopped, a normal user's program that
   creates `\\.\pipe\sevjala` makes `sevjala status` refuse with "the program answering on
   \\.\pipe\sevjala is not the Sevjala service".
7. **Windows 7:** the tray draws its icon and menu on a real Windows 7 PC (go-win7 build).

## M6 — dashboard

Exit criterion: the dashboard matches `sevjala status` live.

Already covered without real PCs: the uiproxy tests (one-time link, per-port cookie, Host check,
no session → 401, `/api` reaches the agent without the cookie, changes need the dashboard header,
a stopped agent is named, `/events` follows changes and says when the agent stops, it stops once
the tab closes, two dashboards keep their sessions); `sevjala ui` opens a browser and stops when
idle; the JS helpers (`npm test` in `ui/`) word times and sizes like the CLI and colour the
verdict like the tray.

Done on macOS (2026-10-17) in a browser against a hand-started controller (MariaDB 10.4 in Docker)
and two backup PCs: the verdict, database card, next backup and backup PCs matched `sevjala
status`; stopping one PC turned the page yellow and its line red and dashed within the controller's
45 s, with "1 copy waiting" after Back up now; starting it again turned everything green without a
reload; a backup PC's own dashboard; two dashboards in one browser; dark mode; a 390 px wide window;
"lost its connection" after `sevjala ui` stopped. After the review fixes (D-083..D-085): starting from a page on another
site, the forwarding page landed on the dashboard with its session; the line labels overlap no box;
the stopped page went grey "Not connected".

Still to do:

1. **Windows controller:** `sevjala ui` from an ordinary Command Prompt opens the default browser;
   the dashboard matches `sevjala status`. The tray's "Open dashboard" opens it too (no console
   window stays open). Closing the tab: within about a minute `sevjala.exe ui` is gone from Task
   Manager.
2. **Back up now** in the dashboard starts a backup; the page updates by itself when it is done.
3. **Mac node:** `sevjala ui` there shows what it holds and when the controller last checked in.
   The browser opens on a file `open.html` that moves on to the dashboard; `ps -ef | grep token` as
   another account shows no token.
4. **Windows 7:** the dashboard renders in Chrome 109 or Firefox 115. With Internet Explorer as the default
   browser the page says it is too old and names Chrome, Firefox or Edge.
5. **Admin-only from the page:** nothing in M6 needs admin; an admin-only answer would say to use
   the command line, which asks for the rights.

## Where a backup came from (D-086)

Done on macOS (2026-10-17): a test node holding backups from before this change shows
`controller  <id> (its name shows with its first backup)` and the id in the FROM column of
`backups list`. The controller's name reaching each node's manifests is covered by
`TestBackupNowReachesThreeNodes`; the node's `controller` and `sources` by
`TestNodeAgentNamesItsControllers`.

Done on the real PCs (2026-10-17, build `0.3.0-alpha.3+1` on the Windows controller and the Mac node):
before a new backup the Mac showed the controller by its id; after Back up now on Windows the
new backup arrived verified and `status` showed `controller  <Windows PC name>, paired …, checked in
…`, with that name in FROM for all 22 backups, the older ones included.

Still to do, after `sevjala update` on the controller and on the node:

1. **Dashboard:** the node's dashboard names the controller on the "The controller" card.
2. **Former controller:** pair the node with a second controller; the first one's backups stay,
   `status` shows `also holds  N backups from <first controller>, a controller this PC is no
   longer paired with`, and `backups list` marks them `(former)`.

## Icon, Start menu entry and the service state for normal users (D-087, D-088)

Done on the Windows controller (2026-10-17, build `0.3.0-alpha.3+3`): the Windows tests of `svc`
ran on the real PC (the status query asked for start and stop before the fix, and passes now);
`sevjala update` (run twice: the first update runs the old updater) created
`C:\ProgramData\Microsoft\Windows\Start Menu\Programs\Sevjala.lnk` with target `sevjala.exe`,
arguments `tray --show`, the exe's icon and a minimised window; the exe carries the new icon.

Done on the Windows desktop by the owner (2026-10-17, build `0.3.0-alpha.3+5`), as a normal user:
`sevjala status` in an ordinary Command Prompt shows `service  running`; the Start menu entry
(found by searching "Sevjala", with the new icon) starts the tray after "Quit tray" and opens the
dashboard when the tray runs; after signing out and in the tray starts by itself; the dashboard's
tab and header show the new icon. Two bugs found on the way and fixed: cobra's "open cmd.exe"
splash stopped anything Explorer started (the Start menu entry and the Run key at login), and
"Open status" opened a window that closed at once (Go's exec gave the child NUL as stdin and
stdout; `TestNewConsoleIsTheChildsConsole` reproduced it on the PC). After `sevjala update` the
running tray restarted itself on the new exe, and "Open status" kept its window open.

## M7 — every operation from the dashboard (D-089..D-094)

Exit criterion: every CLI operation on the controller is doable from the browser, by a normal
user, with administrator rights only for the dump program and restoring from a file.

Already covered without real PCs: Go tests for the amended admin table, `confirm_required` on
policy changes (including a PC that is offline), forced removal and replace, the last-good rule in
retention and removal, the caller account and `by` in the log (the pipe test ran on the Windows
PC), `GET /logs`, `GET /db` without the password; the JS helpers (`npm test` in `ui/`).

Done on macOS (2026-10-17) in a browser against a hand-started controller (MariaDB 10.4 in Docker)
and two backup PCs: the Backups matrix with a column per PC; the export download is named
`shop_<date>_<time>.sql.gz`; a restore into a new database finished with its table count; replace
the live database asked for `shop` in a dialog (the button stayed off for `sho`), backed it up
first and loaded it; lowering a schedule's keep asked for its name and named how many backups and
which PCs; Logs showed each of these with `by` = the account; changing the port on the Database
screen blocked Save until a password was typed.

Done on the Windows controller over ssh (2026-10-17, an elevated session, so it says nothing about
UAC): `sevjala update --from` installed the build; `policy set manual --keep 1` named the 12
backups and the PC it would delete them on and asked to type `manual`, and a wrong word left keep
at 10; `backups export latest --db … --to <folder>` wrote the file and its manifest; the log
holds `backup_exported` with `"by":"<PC>\\<user>"`.

Still to do:

1. **Windows controller, normal user:** in an ordinary Command Prompt (no UAC prompt for any of
   these): `sevjala policy set daily --keep 1` asks to type `daily` when backups would go;
   `sevjala backups export latest --to %USERPROFILE%\Desktop`; `sevjala pair` with a node's code.
   `sevjala db set --dumper …` and `sevjala restore --file …` still ask for administrator rights.
2. **Every screen on the Windows PC** (`sevjala ui`): Backups (Back up now, Export downloads two
   files), Restore (into a new database; replace asks for the database name), Backup PCs (Find,
   Add by address, Pair, Check, Remove; Remove of a PC with the only copies asks for "Remove
   anyway", then its name), Schedules (Add, Change with a preset, Remove, Clean up now), Database
   (Detect, choose databases, Save), Logs (filter by level and text; `by` shows `PC\user`).
3. **Last good copy:** with one backup PC only and the controller's own copy gone, `sevjala
   agents remove <pc> --force` is refused naming the database.
4. **Windows 7 browser:** the new screens render in Chrome 109 / Firefox 115 (the dialog uses
   `<dialog>`).
