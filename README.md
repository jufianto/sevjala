# Sevjala

LAN-first database backup for MySQL/MariaDB (XAMPP first), shipped as one Go binary for
Windows 7+, macOS and Linux. A Controller on the database server dumps, verifies and pushes
backups over mutual TLS to paired backup PCs (Nodes); everything is operated from a CLI and
tray, with a local browser dashboard later. No cloud, no Internet.

**Status:** pre-alpha. P0–M3 code done (scheduled backups, retention), manual smokes pending, see [docs/PROGRESS.md](docs/PROGRESS.md).

## Try it

**Windows:** download `sevjala-win7-amd64.exe` (64-bit Windows 7 to 11) or `sevjala-win7-386.exe`
(32-bit Windows 7) from [Releases](https://github.com/jufianto/sevjala/releases) and double-click it.
Setup asks for administrator rights, then whether this PC runs the database (controller) or keeps
backups (node); it installs to `C:\Program Files\Sevjala`, starts the service and shows the next
step. Double-click it again later to update, show a pairing code or uninstall. `sudo ./sevjala
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
# on the controller (sudo / Administrator prompt)
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
sevjala policy list                    # frequent, hourly, daily, monthly, manual (backup now)
sevjala policy add weekly --schedule @weekly --keep 4
sevjala policy set frequent --keep 6 --to pc-2,pc-3
sevjala retention run --dry-run        # what would be deleted or sent, and why
sevjala backups list --all             # includes expired backups
```

## Commands

Every command explains itself with examples: `sevjala <command> --help`.

| Group | Commands |
|---|---|
| Install & status | `setup`, `service install/start/stop/status/uninstall`, `doctor`, `version` |
| Connect PCs | `discover`, `code` (on the backup PC), `pair`, `agents`, `agents add/remove`, `ping` |
| Database | `db detect`, `db set` |
| Backups | `backup now`, `backups list [--matrix]` |
| Schedules & cleanup | `policy list/add/set/remove`, `retention run [--dry-run]` |

Build: `make build` (host), `make build-all` (every asset incl. Windows 7), `make test`,
`make test-integration` (real MariaDB 10.4 in Docker), `make lint`.

Design: [docs/BLUEPRINT.md](docs/BLUEPRINT.md) · docs index: [docs/README.md](docs/README.md)
