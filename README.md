# Sevjala

LAN-first database backup for MySQL/MariaDB (XAMPP first), shipped as one Go binary for
Windows 7+, macOS and Linux. A Controller on the database server dumps, verifies and pushes
backups over mutual TLS to paired backup PCs (Nodes); everything is operated from a CLI and
tray, with a local browser dashboard later. No cloud, no Internet.

**Status:** pre-alpha. P0–M3 code done (scheduled backups, retention), manual smokes pending, see [docs/PROGRESS.md](docs/PROGRESS.md).

## Try it

Put the binary in a permanent folder first; the service runs it from there.

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

Build: `make build` (host), `make build-all` (every asset incl. Windows 7), `make test`,
`make test-integration` (real MariaDB 10.4 in Docker), `make lint`.

Design: [docs/BLUEPRINT.md](docs/BLUEPRINT.md) · docs index: [docs/README.md](docs/README.md)
