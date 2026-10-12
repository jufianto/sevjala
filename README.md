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

Build: `make build` (host), `make build-all` (every asset incl. Windows 7), `make test`, `make lint`.

Design: [docs/BLUEPRINT.md](docs/BLUEPRINT.md) · docs index: [docs/README.md](docs/README.md)
