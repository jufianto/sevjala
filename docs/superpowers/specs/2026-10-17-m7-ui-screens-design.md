# M7 UI screens: design

Date: 2026-10-17. Owner: @jufi. Status: draft for owner review.

## Intent

Everything an operator does with the `sevjala` CLI on the controller can be done from the
dashboard (`sevjala ui`), by the normal user signed in at that PC, without administrator rights.
Office operators are not database admins; the browser is where they look, so it is where they
act. The roadmap's exit criterion: every CLI operation is doable from the browser.

Two owner decisions shape the milestone (chat, 2026-10-17):

1. **Admin only where it is really necessary.** A normal user can do everything except what the
   operating system itself reserves for administrators, or what would hand them the service's
   own system rights. This amends AD-20 (below). Data is protected by typed confirmation, a
   record of who did what, and a last-good-copy guard instead of by admin rights.
2. **Manager-ready.** Every screen talks only to the agent's local API through the dashboard's
   `/api` relay. A later milestone ("Managers", see `docs/PROGRESS.md`) points that relay at the
   controller from another PC; no screen is rebuilt for it.

## Goals

- Screens for backups, restore, backup PCs, schedules, database and logs on the controller.
- AD-20 amended: the admin-only list shrinks to the necessary cases, server and CLI alike.
- Safety that does not depend on admin: typed confirmation, an audit trail naming the account,
  and a guard that never leaves a database without a verified copy.
- Export no longer needs admin; restoring from a file stays admin (it runs SQL the caller supplies).

## Non-goals

- Setup, update, uninstall, service start/stop, the tray and `version` in the browser: they set
  up the PC itself. `code` (node) and the node's own dashboard screens stay as in M6.
- `doctor` in the browser (the verdict covers its everyday use; the CLI keeps it).
- Managers, remote access, anything off 127.0.0.1 (AD-14 unchanged).
- Users and roles inside Sevjala; the OS account is the identity.

## Anti-goals (a failure even if it works)

- A normal user able to make the service run a program or read a file of their choosing.
- A destructive action (deleting backups, overwriting the live database, dropping a PC's only
  copies) that happens on one click, or that leaves no trace of who did it.
- A database left with no verified backup anywhere because of a cleanup or a removal.
- Screens that call anything but `/api/*` (they would not work for managers).

## Permission model (AD-20 amended)

Proposed AD row text (replaces AD-20's admin list; the per-request caller check stays):

> AD-20 (amended M7): The local API is open to every interactive account on the PC (Windows
> interactive users, macOS/Linux members of the `sevjala` group, root/SYSTEM); system and
> service accounts other than root/SYSTEM are refused as before. Administrator rights are
> required only (a) where the OS requires them: setup, update, uninstall, service
> install/start/stop/uninstall, firewall, start-at-login and Start menu entries; and (b) where
> the request would make the service run, read or execute something the caller supplies: `db set
> --dumper` (a program) and `restore --file` (a file of SQL the service runs with the stored
> database password). Everything else is a normal-user action. Destructive actions need typed
> confirmation; every change is logged with the caller's account; retention and removals never
> leave a configured database without a verified copy.

What changes, per request (protocol "Local API" admin table):

| Request | Before | After |
|---|---|---|
| `POST /pair`, `POST /code`, `DELETE /agents/{agent}` | admin | user (`DELETE` with `force` needs confirmation) |
| `GET /held/{id}` (export) | admin | user |
| `POST /backup/now` with a schedule's tag | admin | user |
| `PUT /db` host/port change | admin | user; a host **or port** change needs the password again (400 otherwise), so the stored password never goes to a server the caller picks |
| `PUT /db` `dumper` | admin | **admin** (the service would run that program) |
| `POST /policies` over a tag in use | admin | user (needs confirmation when the plan deletes something) |
| `PATCH /policies/{name}` keep down, destination dropped, schedule/enabled | admin | user (keep down / destination dropped need confirmation when the plan deletes something) |
| `POST /restore` with `file` | admin | **admin** (the service runs the file's SQL with its own rights and the stored password) |

The CLI's client-side admin checks follow: `backups export` no longer checks `svc.IsAdmin()`;
`setup`, `update` and `service *` keep `requireAdmin`; `db set --dumper` and `restore --file`
keep the elevated re-run on `admin_required`. `db set --port` without `--password` is refused
like `--host`. On macOS/Linux "normal user" means a member of the `sevjala` group (socket
`0660 root:sevjala`), as today.

## Safety without admin

### Typed confirmation

One rule for the CLI and the browser: the action names a word, the operator types it.

| Action | Word | When |
|---|---|---|
| Replace the live database | the database name | always (exists today, unchanged) |
| Lower `keep`, drop a destination, or add a schedule over a tag in use | the schedule name | only when a dry run of the change deletes at least one backup; the prompt shows how many and which PCs |
| Remove a backup PC with `force` | the PC's hostname | always (it holds a backup's only verified copy) |

Server side the request carries `confirm` with the word; a destructive request without it is
409 `{"error":"…","code":"confirm_required","word":"<word>","deletes":N}`. The CLI prompts as
`restore --replace-production` does today and takes `--confirm <word>` for scripts. The browser
shows a dialog with the count and a text field; the button stays disabled until the word matches.
`POST /policies` and `PATCH /policies` gain `dry_run` so the CLI and the browser can show what a change would
delete before asking.

### Who did it

The local API records the caller's account per connection, next to the admin flag:
Windows: the pipe client's token user, as `DOMAIN\user` (`LookupAccountSid`); macOS/Linux:
the peer uid's user name (`os/user`), or the uid when it has none. Every change event the agent
logs (pair, remove, policy added/changed/removed, database set, backup now, restore, retention
run, export) gains `"by":"<account>"`. The Logs screen shows it.

### Last good copy

New rule in retention and in `DELETE /agents/{agent}?force=true`: never leave a configured
database with zero verified copies on reachable or unreachable nodes and staging combined.
Retention keeps the newest verified backup of each configured database (on every node holding
it) whatever the policies say; the plan names the reason (`ReasonLastGood`). A forced removal
of the PC holding the only verified copy of a database is refused with 409 naming the database;
a forced removal that only drops copies other PCs also hold proceeds after confirmation.
Covered by table tests in `retention` and `store`, including the two paths found in review:
dropping a destination while the wanted backup is verified nowhere, and `force` removal.

### Restore from a file and export

- **Restore from a file** stays as today (`POST /restore` with `file`, admin; the CLI re-runs
  elevated). A normal user cannot have the service run SQL of their choosing (anti-goal 1): a
  crafted file could run shell commands through the `mysql` client on macOS/Linux, write files
  with `INTO OUTFILE`, or `USE` the live database and skip the typed confirmation. The Restore
  screen shows the option with: "Restoring from a file needs an administrator: run `sevjala
  restore --file <path>` from an administrator prompt."
- **Export**: unchanged route (`GET /held/{id}` streams the bytes); it just no longer needs admin.
  The browser downloads `.sql.gz` and `.json` with `Content-Disposition`; the CLI writes them
  as the user, as today.

## Screens (controller)

Navigation: Dashboard · Backups · Restore · Backup PCs · Schedules · Database · Logs.
Every screen updates from `/events` or polls its own `GET` while visible; every action shows the
agent's own message on failure. Phone width and dark mode as in M6.

- **Dashboard** (M6): unchanged.
- **Backups**: one row per backup (newest first), one column per backup PC, cells show the copy
  state (verified, waiting, transferring, failed, deleted). Filters: database, schedule (tag),
  include expired. **Back up now** with a database and schedule choice. Row actions: **Restore…**
  (opens Restore with it chosen), **Export** (downloads both files; offered for backups the
  controller still holds in staging, as `GET /held` lists). Source: `GET /backups[?all=1]`, `GET /agents`.
- **Restore**: pick a backup (from Backups, or a list here); restoring from a file shows the administrator
  note above.
  Mode: into a new database (name field, CLI's default name) or **replace the live database**
  (typed confirmation). **Preview** shows the dry-run plan; **Start** runs it; progress and the
  result come from `GET /restore` every 2 s while it runs.
- **Backup PCs**: the M6 diagram, plus **Find PCs on this network** (`POST /discover`),
  **Add by address** (`POST /agents`), **Pair** (code + fingerprint prefix shown by `sevjala
  code` on the PC, `POST /pair`), **Check** (`POST /agents/{agent}/ping`), **Remove** (with
  the force/confirmation rules above).
- **Schedules**: table of policies (name, when, keep, destinations, on/off, next run); **Add**,
  **Change**, **Remove**; schedule field with presets (hourly, daily at…, weekly, monthly) and
  a raw cron field. Lowering keep or dropping a destination shows the dry-run deletions and asks
  for the schedule name. **Clean up now** shows the retention dry run, then runs it.
- **Database**: **Detect** (`POST /db/detect`) lists the MySQL tools found and the databases;
  checkboxes choose what to back up; host, port, user and a write-only password field
  (`PUT /db`). The dump tool field is shown read-only with: "Changing the dump tool needs an
  administrator: run `sevjala db set --dumper <path>` from an administrator prompt."
- **Logs**: the newest 500 entries, newest first: time, level, event, message, `by`. Filter by
  level (info, warn, error) and text; **Refresh**. Source: new `GET /logs?limit=N` reading the
  JSON lines of `sevjala.log` and its rotations, newest first, `limit` ≤ 2000.

## Wire and format changes (docs/protocol.md)

- Admin table rewritten as above; `confirm`/`confirm_required`/`word`/`deletes` on the
  destructive routes; `POST /policies` and `PATCH /policies` `dry_run`.
- `PUT /db`: a port change needs the password, like host and user.
- `GET /logs?limit=N` (new, both roles): `{"entries":[{"time","level","event","msg","by",…}]}`.
- Log events: the `by` attribute (absent on events the agent starts itself, such as a scheduled backup).

## Constraints

- AD-01..AD-19 unchanged; AD-20 amended as above (owner approved 2026-10-17).
- Frontend: Svelte 5 + Vite, exact pins, `build.target` chrome109/firefox115, hash routing,
  pure logic in `src/lib/*.js` tested with `node --test` (D-080). No new npm dependencies.
- Go: no new dependencies. Every route change gets Go tests, including a real-pipe/socket test
  for the caller account; admin table cases in `coord` updated.
- The dashboard proxy and its guards (AD-14, D-078..D-085) unchanged; export downloads go
  through `/api` like every other request.
- Secrets: the database password is never sent back to the browser or logged.

## Calls made in review (owner to check)

- Restoring from a file stays admin-only (it runs caller-supplied SQL with system rights).
- A MySQL port change needs the password again, like a host change.
- Adding a schedule over a tag in use is confirmed like lowering `keep`.

## Left to the builder

- Component structure, styling within the M6 look, wording beyond the copy fixed above.
- How `GET /logs` reads rotations efficiently, as long as it returns the newest entries first.
- Cron presets' exact list.
