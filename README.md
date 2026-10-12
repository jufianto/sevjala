# Sevjala

LAN-first database backup for MySQL/MariaDB (XAMPP first), shipped as one Go binary for
Windows 7+, macOS and Linux. A Controller on the database server dumps, verifies and pushes
backups over mutual TLS to paired backup PCs (Nodes); everything is operated from a CLI and
tray, with a local browser dashboard later. No cloud, no Internet.

**Status:** pre-alpha. P0 (service + local API) in progress, see [docs/PROGRESS.md](docs/PROGRESS.md).

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
sevjala code                           # prints a 6-digit code, valid 10 minutes
# back on the controller
sevjala pair <agent> --code 123456     # <agent>: id prefix from discover, or hostname
sevjala ping <agent>
sevjala agents
```

Build: `make build` (host), `make build-all` (every asset incl. Windows 7), `make test`, `make lint`.

Design: [docs/BLUEPRINT.md](docs/BLUEPRINT.md) · docs index: [docs/README.md](docs/README.md)
