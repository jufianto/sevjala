# Sevjala

LAN-first database backup for MySQL/MariaDB (XAMPP first), shipped as one Go binary for
Windows 7+, macOS and Linux. A Controller on the database server dumps, verifies and pushes
backups over mutual TLS to paired backup PCs (Nodes); everything is operated from a CLI and
tray, and a local browser dashboard (`sevjala ui`). No cloud; the only Internet use is `sevjala update`, when you run it.

**Status:** pre-alpha. P0–M6 code done (scheduled backups, retention, restore, tray, dashboard), manual smokes pending, see [docs/PROGRESS.md](docs/PROGRESS.md).

## Try it

**Windows:** download `sevjala-win7-amd64.exe` (64-bit Windows 7 to 11) or `sevjala-win7-386.exe`
(32-bit Windows 7) from [Releases](https://github.com/jufianto/sevjala/releases) and double-click it.
Setup asks for administrator rights (UAC), then whether this PC runs the database (controller) or keeps
backups (node); it installs to `C:\Program Files\Sevjala`, starts the service and shows the next
step; a tray icon by the clock then shows whether the data is protected (green), needs a look
(yellow) or has a problem (red). Double-click it again later to update, show a pairing code or
uninstall. To update later, run `sevjala update` on each PC: it asks for administrator rights, downloads the newest release
from here, checks it and restarts the service; `--from FILE` installs a file you copied over. `sudo ./sevjala
setup` does the same on macOS and Linux (installs to `/usr/local/bin`).

By hand: put the binary in a permanent folder first; the service runs it from there.

```sh
sudo ./sevjala service install --role node   # Windows: Administrator prompt, no sudo
sudo ./sevjala service status
sudo ./sevjala doctor
sudo ./sevjala service uninstall             # keeps the data dir
```

Pair a node with the controller (same LAN; the node's firewall must allow 17890/udp and
17891/tcp, which `service install` opens on Windows):

```sh
# on the controller
sevjala discover                       # lists nodes; or: sevjala agents add 192.168.1.21
# on the node
sevjala code                           # prints a 6-digit code (valid 10 minutes) and a fingerprint
# back on the controller: the pair command `code` printed
sevjala pair <agent> --code 123456 --fp 3f2a9b1c77de0a41   # <agent>: id prefix or hostname
sevjala ping <agent>
sevjala agents
```

First backup (controller; `mysqldump` from XAMPP or PATH):

```sh
sevjala db detect                      # finds mysqldump, lists databases and their dump mode
sevjala db set --db shop --password    # tested against the server before it is saved
sevjala backup now                     # dumps, then pushes to every paired node; waits
sevjala backups list --matrix          # one column per node
```

The database password is kept in the OS keyring (an encrypted file on Linux without one),
never in `config.toml`.

Unattended (controller): backups run on policy schedules (local time) and each node keeps the
newest N per policy; a node that was off is filled in when it comes back.

```sh
sevjala policy list                    # frequent, hourly, daily, monthly, manual (backup now), pre-restore
sevjala policy add weekly --schedule @weekly --keep 4
sevjala policy set frequent --keep 6 --to pc-2,pc-3
sevjala retention run --dry-run        # what would be deleted or sent, and why
sevjala backups list --all             # includes expired backups
```

Restore (controller): into a new database by default, so the live one is never touched. Step by
step: [docs/restore-guide.md](docs/restore-guide.md).

```sh
sevjala restore latest                 # into shop_restored_<date>; prints table count and time
sevjala restore BK-20261016-101500-A8F2 --into shop_check
sevjala restore latest --replace-production   # backs shop up first, then asks you to type "shop"
sevjala restore --file D:\export\shop\2026-10-16_10-15-00.sql.gz   # a file from backups export
```

## Commands

Every command explains itself with examples: `sevjala <command> --help`.

| Group | Commands |
|---|---|
| Install & status | `status`, `ui` (dashboard in the browser), `tray`, `setup`, `update [--from FILE]`, `service install/start/stop/status/uninstall`, `doctor`, `version` |
| Connect PCs | `discover`, `code` (on the backup PC), `pair`, `agents`, `agents add/remove`, `agents name <pc> "<name>"`, `ping`, `agents manager <pc> on\|off` |
| Database | `db detect`, `db set` |
| Backups | `backup now`, `backups list [--matrix]`, `backups export <backup\|latest\|all> --to FOLDER` (copy out; the copy belongs to you), `restore <backup\|latest> [--into NAME \| --replace-production]`, `restore --file PATH`, `restore --status` |
| Schedules & cleanup | `policy list/add/set/remove`, `retention run [--dry-run]` |

Most commands need no administrator rights. The few that do (setup, service, update,
`db set --dumper` and `restore --file`) ask for them themselves: UAC on Windows, `sudo` on
macOS/Linux. Changes that delete backups (a schedule keeping fewer, `agents remove --force`,
`restore --replace-production`) ask you to type a word first, or take `--confirm WORD`; the log
records which account made each change. Everything the commands do on the controller can also be
done in the dashboard: `sevjala ui`.

**Names:** a backup PC shows the name it reports for itself. Give it a clearer one with
`sevjala agents name <pc> "Office PC"` (or Rename… on the dashboard's Backup PCs screen);
every screen and command then shows and accepts that name, and `--clear` goes back.

**Managers:** an administrator on the controller can make a backup PC a manager
(`sevjala agents manager <pc> on`). On that PC, `sevjala -c <command>` (short for `--controller`: status, agents,
backup, restore, policy, …) and `sevjala ui -c` (or the tray's "Open controller
dashboard") work as if you sat at the controller, over the connections the controller already
opens: the backup PC still never connects out, and nothing new listens on the database PC.
Steps that need administrator rights are still done on the controller itself.

Build: `make build` (host), `make build-all` (every asset incl. Windows 7), `make test`,
`make test-integration` (real MariaDB 10.4 in Docker), `make lint`.

Design: [docs/BLUEPRINT.md](docs/BLUEPRINT.md) · docs index: [docs/README.md](docs/README.md)
