# M5 Operator comfort: design

Date: 2026-10-17 · Milestone: M5 Operator comfort ([Roadmap](../../BLUEPRINT.md#roadmap)) · Owner: @jufi

Blueprint scope: `fyne.io/systray` tray with state colour + menu, `doctor`, one-screen `status`.
Sections: Components (Tray row), Coordination → Local API, Security model → Local API row.
The owner decided the admin model in chat on 2026-10-17 (below, "Owner decisions"). Every other
point the blueprint leaves open is marked **my call** and becomes a `docs/decisions.md` row
(D-066 on).

## Intent

The office person next to the server is not a database administrator. They must be able to see
at a glance whether their data is protected, and do the daily things (check, back up now,
restore) without hunting for "Run as administrator". Today every command needs an
Administrator prompt (Windows) or the `sevjala` group (Unix), and `sevjala status` shows only
whether the service runs, not whether backups are good.

**Exit criterion** (Roadmap): a non-engineer can tell protection is healthy from the tray alone.

## Goals

1. **Health**: the agent computes one health verdict (`ok`, `warning`, `problem`) with a
   headline and a list of plain-language attention lines, from data it already has. The CLI,
   the tray and later the UI Dashboard all show this same verdict.
2. **One-screen `sevjala status`**: service, agent, the verdict, per database the last good backup
   and its copies, the next scheduled backup, each backup PC's state, free space, attention
   lines. A node shows what it holds and when the controller last contacted it.
3. **Tray**: `sevjala tray`, started at login for every user, shows the verdict as an icon
   colour and the status lines in its menu, with "Back up now" (controller) and "Open status".
4. **Fewer admin prompts** (owner decision): daily work needs no admin; the few admin actions
   ask for it themselves instead of failing.
5. **`doctor` without admin** runs every check it can and says which ones need admin.

## Non-goals

- Browser UI, `/events` SSE, a logs screen (M6/M7). The tray has no log viewer: the log folder
  is admin-only and reading it belongs to the M7 Logs screen.
- Notifications or balloon pop-ups (Phase 3, AD-17).
- Admin actions from the tray: none of its menu items needs admin in M5.
- Linux tray in the release assets (see Build).

## Owner decisions (2026-10-17, in chat)

Rule: admin only where the OS itself requires it, or where an action could quietly send the data
away or destroy backups.

| No admin (allowed callers, below) | Admin |
|---|---|
| `status`, `backups list`, `doctor`, `agents`, `policy list`, `restore --status` | `setup`, `service install/uninstall/start/stop`, `update` (the OS requires it) |
| `backup now`, `retention run` | `db set --dumper PATH` (the service runs that program with full rights) |
| `restore` into a new database **and** `--replace-production` (it has its own typed confirmation and pre-restore backup; on XAMPP any user can already drop the database in phpMyAdmin) | `pair` a backup PC (a paired PC receives full copies of the database) |
| `backups export`, `db set` (host, port, user, password, databases), `db detect` | policy changes that **delete** backups (see Admin routes) |
| | `restore --file PATH` (**my call**, see Admin routes; used only on a new controller, during setup) |
| `agents remove`, `discover`, `ping`, policy changes that delete nothing | |
| the tray, later the browser UI | |

**Allowed callers** are people logged in at that PC, not background services:
- **Windows:** users logged on interactively, at the keyboard or over Remote Desktop.
  Service accounts (IIS, NETWORK SERVICE) are refused.
- **macOS/Linux:** root and members of the `sevjala` group. `setup` adds the user who ran it
  with sudo (already built). System accounts such as `www-data`, `_www` and `mysql` are not
  members and are refused.

**Admin actions ask for admin themselves:**
- **Windows:** a non-elevated `sevjala` that needs admin re-runs the same command through the
  UAC prompt (`ShellExecuteEx`, verb `runas`), the way `setup` already does.
  - The elevated run happens in a new window, which ends with "Press Enter to close".
  - The first window waits for it and then prints `finished in the administrator window (exit
    N)`.
  - If the operator answers No: `Cancelled: administrator rights were refused`, exit 1,
    nothing changed.
- **macOS/Linux:** the CLI re-runs itself as `sudo <same exe> <same args>` in the same
  terminal. If `sudo` is missing or fails, it prints `this needs administrator rights: run
  with sudo` and exits 1.

## Design

### Local API: who is calling (new AD-20, amends AD-13; the behaviour was accepted by the owner in chat, the row is added with this milestone)

AD-13 says "OS ACL is the auth". M5 makes the OS ACL decide *who may connect*, and the agent
decide per request *who may do what*:

- **Windows pipe DACL:** SYSTEM and Administrators full control, plus Interactive users (IU)
  with read and `FILE_WRITE_DATA` only.
  - Not generic write: on a pipe, generic write includes `FILE_CREATE_PIPE_INSTANCE`, which
    would let any user open a rival instance of `\\.\pipe\sevjala` and catch an admin's
    request (e.g. `PUT /db` with the password). The builder writes the exact SDDL and a test
    for it.
  - A non-elevated administrator's token carries Administrators as deny-only, so it connects
    through the IU entry as a normal caller.
  - Custom (dev/test) endpoints keep the default DACL, as today.
- **Pipe squatting (my call):** when the service is stopped, any user can create
  `\\.\pipe\sevjala` first. On the default endpoint the Windows client therefore checks that the
  pipe is owned by Administrators or SYSTEM before sending anything (as built: a normal user
  cannot open the SYSTEM service's process token, but can read the pipe's owner, and cannot
  create a pipe owned by Administrators). If it doesn't, it refuses with `the program answering on
  \\.\pipe\sevjala is not the Sevjala service`. The Unix socket lives in root-owned
  `/var/run`, so this check is not needed there.
- **Unix socket:** unchanged, root:`sevjala` mode 0660.
- **Per connection, the server learns whether the caller is admin** and stores it in the
  request context (`http.Server.ConnContext`):
  - Windows: the client process's token is elevated, or the client is SYSTEM.
    `GetNamedPipeClientProcessId` → `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` →
    `OpenProcessToken`, or `ImpersonateNamedPipeClient`; the builder picks one. Only
    Windows 7 APIs; `golang.org/x/sys/windows`, already in `go.sum`.
  - Linux: `SO_PEERCRED` uid 0. macOS: `LOCAL_PEERCRED` uid 0. Both via
    `golang.org/x/sys/unix`.
  - If the caller's identity cannot be read, the caller is treated as not admin.
- **Admin routes return 403** with body `{"error": "...", "code": "admin_required"}`. The
  message names the action, e.g. `pairing a backup PC needs administrator rights`. The CLI
  turns this code into the elevation above and re-runs the command. Nothing has changed on
  the agent at that point, so re-running is safe.

**Admin routes** (everything else is allowed for any caller that can connect):

| Route | Admin when |
|---|---|
| `POST /pair` (controller) | always |
| `POST /code` (node: show a pairing code) | always. **My call:** pairing a node to a new controller lets that controller delete the node's backups, so both halves of pairing are admin |
| `PUT /db` | the body sets `dumper` |
| `PATCH /policies/{name}` | `keep` goes down, or `to` drops a backup PC (including "all" → a list); both make retention delete copies |
| `POST /policies` | backups carrying that name as a tag exist. A tag without a policy keeps its backups forever, so a new policy with a low `keep` would delete them |
| `POST /restore` | the body sets `file` (**my call**: the agent runs as SYSTEM and opens that path itself, so a normal user could make it read and load a file they cannot read) |

`DELETE /policies/{name}` is **not** an admin route: removing a policy deletes nothing, because
its tag then keeps its backups (retention rule "a tag without a policy keeps its backups").

**`PUT /db` changing host or user (my call):** without a new password in the same request, the
saved password is dropped, and the CLI asks for it. Otherwise a normal user could point the
agent at a MySQL server they control and receive a login attempt made with the saved password.

Commands that run locally without the agent (`setup`, `service install/uninstall/start/stop`,
`update`) keep their own `IsAdmin` check. When it fails they elevate as above instead of
failing.

### Health (computed by the agent)

The local `GET /status` gains a `health` object, computed on each call from the store, the poll
snapshot and the staging disk. It never runs `mysql` or `mysqldump`: the tray polls it, and it
must answer in milliseconds.

```json
"health": {
  "level": "ok | warning | problem",
  "headline": "Protected: last backup 10:20 (7 min ago) is on 3 backup PCs",
  "items": [{"level": "warning", "text": "PC-2 has not answered since 08:12; 2 copies wait for it"}],
  "databases": [{"name": "shop", "last_good": "BK-…", "last_good_at": "…", "copies": 3, "planned": 3, "last_failed_at": "…", "last_error": "…"}],
  "next_backup_at": "…",
  "agents": [{"hostname": "PC-2", "state": "offline", "last_seen": "…"}],
  "free_bytes": 0,
  "controller_contact_at": "…"
}
```

`databases`, `next_backup_at` and `agents` are for the controller; `controller_contact_at` is
for a node. The **level** is the worst item's level; with no items it is `ok`. **My call** on
the rules:

**Controller: problem (red)**
- No backup PC paired: `Not protected: no backup PC is paired; backups exist only on this PC`.
- A configured database has no good backup yet, or its newest good backup is older than
  **2×** the shortest interval of the enabled schedules that back it up (minimum 1 h).
  - A *good* backup: the dump finished, and at least one backup PC holds a verified copy.
  - Text: `No backup of shop since 2026-10-16 01:00`.
- The newest backup attempt of a database failed: `The last backup of shop failed at 10:20:
  <error>`. It clears when a later backup of that database succeeds.
- A `replace-incomplete-<db>` marker exists: `shop was left partly loaded by a failed
  restore; run: sevjala restore <id> --replace-production`.
- Staging free space is under 1 GiB, or under twice the newest backup of the largest
  database, whichever is larger (**my call**; the agent has no floor today and only sees disk
  full after the fact): `Disk almost full on this PC: <free> left`.

**Controller: warning (yellow)**
- A backup PC is `offline` or `error`: `PC-2 has not answered since 08:12`, with `; N copies
  wait for it` when copies are pending.
- The newest backup of a database is `partial` for longer than 30 min, for a reason not
  already covered above.
- A copy is `corrupt` and cannot be replaced (no good source left).
- No enabled schedule: `No schedule is on: backups run only with 'backup now'`.
- No database configured: `No database chosen: run sevjala db set`.

**Node**
- Problem: the store folder is not writable, or free space is under the node's
  `min_free_bytes`.
- Warning: the store has problems (corrupt files), or no controller has contacted it for
  15 min. Polls come every 15 s (D-040), so this allows for a controller restart or a short
  network drop without a false warning.
  - Text: `The controller has not contacted this PC since 10:21`.
  - Unpaired: `Not paired: run sevjala code here and pair from the controller`.
- `controller_contact_at` is new: the node's LAN server records the time of each
  authenticated controller request, in memory. After an agent restart it is empty until the
  next poll, and the "not contacted" warning waits 15 min after start-up.

**Headline when ok:** `Protected: last backup 10:20 (7 min ago) is on 3 backup PCs`. With
several databases, the oldest of their last good backups. A node: `Holding 34 backups for
SERVER-01; last contact 10:21`. Otherwise the headline is the first worst item's text.

### `sevjala status`

One screen, no admin. Controller example:

```
Sevjala controller on SERVER-01                      WARNING
  service    running (windows-service), agent 0.4.0, up 2d 3h
  shop       last backup 10:20 (7 min ago), 10.8 MiB, on 3 of 3 backup PCs
  next       10:30 (frequent)
  backup PCs PC-1 online · PC-2 offline since 08:12 · PC-3 online
  disk       41.2 GiB free here
  attention  PC-2 has not answered since 08:12; 2 copies wait for it
```

- The level word is `OK`, `WARNING` or `PROBLEM`. Nodes show their own lines: holds,
  controller, last contact, disk.
- When the agent does not answer: the service line plus today's hint (start it / install it),
  with level `PROBLEM`.
- Exit code 0 for ok or warning, 1 for problem or no answer. This replaces today's "agent
  answering" check, so it stays script-friendly.
- **My call:** no `--watch` flag; the tray covers watching.

### Tray

`sevjala tray`: one process per logged-in user, as that user, never admin.
- **Single instance:** a second start in the same session exits quietly. Windows: a named
  mutex scoped to the session. Unix: a lock file in the user's runtime/temp folder.
- **Polling:** local `GET /status` every 15 s. **My call.**
- **Icon:** a filled circle in green (ok), yellow (warning), red (problem), or grey (the
  service is not answering, or the tray may not connect).
  - The icons are small embedded files (ICO for Windows, PNG elsewhere), committed in the
    `tray` package.
  - The tooltip is the headline.

Menu (controller):

```
Protected: last backup 10:20 (7 min ago) is on 3 backup PCs   (disabled)
shop: 10:20, on 3 of 3 backup PCs                             (disabled, one per database)
PC-2 has not answered since 08:12                             (disabled, one per attention item, max 5)
─────
Back up now
Open status
─────
Quit tray
```

- **Back up now** calls `POST /backup/now`. While it runs, the item reads `Backing up… (see
  Open status)` and is disabled; it comes back when the backup leaves `dumping`.
- **Open status** opens a console window running `sevjala status`, ending with "Press Enter to
  close". Windows: a new `cmd` window. macOS: Terminal through `osascript`. Linux: whatever
  `x-terminal-emulator` finds; the item is hidden when none exists.
- **The menu on a node:** headline, disk line, Open status, Quit tray.
- **Grey:** `Sevjala service is not running` (or `This account may not use Sevjala`), Open
  status, Quit tray.

**Start at login** (written by `setup` and `service install`, which already run as admin;
removed by `service uninstall`):
- Windows: value `Sevjala Tray` under `HKLM\Software\Microsoft\Windows\CurrentVersion\Run`, =
  `"<install dir>\sevjala.exe" tray`.
- macOS: `/Library/LaunchAgents/dev.jufi.sevjala.tray.plist`.
- Linux: `/etc/xdg/autostart/sevjala-tray.desktop`.

**No console window on Windows (my call):** `sevjala.exe` is a console program, so a tray
started from the `Run` key gets a console window. `sevjala tray` calls `FreeConsole` first
thing, so the window only flashes briefly at login. One binary stays one binary (AD-01). If
the smoke shows the flash bothers people, a GUI-subsystem launcher is a later decision, not M5.

**Trays and updates (my call):**
- A running tray keeps its exe locked on Windows. A tray that sees the installed binary
  replaced (another file at that path, not a version compare) starts it as a new tray and
  exits, so after `update` every tray moves to the new binary within 15 s and releases
  `sevjala.exe.old`.
- `update` also copes with a `.old` that is still locked: it renames it to
  `sevjala.exe.old-<n>` and removes any older ones it can.
- `service uninstall` removes the autostart entry; every tray quits within 15 s once its entry
  is gone, and uninstall retries removing the exe for 20 s. (As built: killing `sevjala.exe`
  processes would also end the setup windows.)

After setup finishes, the tray is started for the user who ran it, **not elevated**:
- The first, non-elevated process (the one that asked for UAC or ran `sudo`) starts it once the
  elevated setup ends successfully. Setup started already elevated does not; the tray appears
  at the next login.

### `doctor` without admin

`doctor` reads `config.toml` and the identity from the locked data folder, so as a normal user
those checks show `skipped: needs administrator (run 'sevjala doctor' as administrator for
these)`. Every API-based check still runs. **My call:** no automatic elevation for `doctor`,
so it never prompts for a read-only check. When the agent does not answer at all, doctor ends
with `run 'sevjala doctor' as administrator for the full check`.

### Build

- **Windows:** the tray is in every Windows binary (pure Go, `CGO_ENABLED=0`), including the
  go-win7 builds. The `tray` build tag gates only the cgo platforms.
- **macOS:** the release builds with `-tags tray` and `CGO_ENABLED=1`. `make build-all` runs on
  a Mac, so both darwin architectures build there. This needs a separate GoReleaser build id.
- **Linux:** as built, the release ships the tray: `fyne.io/systray` talks D-Bus
  (StatusNotifierItem) in pure Go, so it compiles with `CGO_ENABLED=0`. Without a desktop
  session `sevjala tray` fails to reach D-Bus and says so. Only a macOS build without cgo has
  no tray; there `sevjala tray` prints `this build has no tray; on macOS build it with
  CGO_ENABLED=1 and -tags tray` and exits 1.
- `fyne.io/systray` is a blueprint-named dependency. It must be checked to load and draw on
  Windows 7 (go-win7 build). If it calls an API newer than Windows 7 without a fallback, stop
  and report: that is a blueprint-level problem.

### Docs and records

- `docs/BLUEPRINT.md`:
  - AD-20 row (amends AD-13) and the Security model → Local API row: interactive users
    connect; admin routes are checked per request.
  - The Local API section: the socket line (group `sevjala`, already true).
- `docs/protocol.md`: `health` in local `/status`, the `admin_required` 403, the admin-route
  table, node `controller_contact_at`.
- `docs/decisions.md`: D-066 on for every "my call" above. Close the open item "M5: under UAC
  the tray runs un-elevated…".
- `docs/smoke-tests.md` (M5 section), `docs/restore-guide.md` and `README.md`: drop
  "Administrator Command Prompt" where no longer needed.

## Testing

- **Unit, table-driven:**
  - every health rule, with the clock and poll snapshot injected;
  - the admin-route classification (PATCH keep up/down, `to` narrowed/widened, PUT /db
    with/without dumper);
  - CLI elevation is decided by the `admin_required` code.
- **Loopback:** a test endpoint where the caller identity is injected (admin / not admin):
  - each admin route refuses a non-admin with 403 `admin_required`;
  - the same request succeeds as admin;
  - `GET /status` returns `health`.
- **Windows-only:** a test that the default DACL string contains the IU entry and still
  refuses everyone else. The real non-admin connection is a manual smoke.
- **Tray:** the menu model (lines, items, enabled states, colour) is a pure function of
  `localapi.Status` + "not answering", unit-tested. The systray glue stays thin and is covered
  by the manual smoke.
- **Manual smokes (owner's Windows PC and Mac):**
  - a normal (non-elevated) prompt runs status, backup now, restore and export;
  - `pair` raises the UAC prompt; answering No changes nothing;
  - the tray turns yellow when the Mac is switched off, and green again when it is back;
  - red when MySQL is stopped and a backup fails;
  - grey when the service stops;
  - the tray starts at login;
  - Windows 7 is still owed as a real-machine smoke.

## Left to the builder

Package layout inside `tray/`, icon artwork, the exact Windows API sequence for the caller's
token, how the CLI forwards `--data-dir` and other global flags when re-running, wording of the
status lines beyond the examples, and how the poll snapshot exposes "copies waiting per PC".
