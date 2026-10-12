# P0 Foundation — design

Date: 2026-10-12 · Milestone: P0 ([Roadmap](../../BLUEPRINT.md#roadmap)) · Branch: `p0-foundation`

## Goal and exit criterion

A `sevjala` binary that installs itself as an OS service, runs an agent that answers on the
local pipe, and reports that it does — built for every asset in the build matrix.

**Exit:** `sevjala service install && sevjala service status` works on Windows 7, Windows 11,
macOS and Linux.

Blueprint sections in play: Components, Toolchain and build matrix, Repository layout.

## Scope

In:
- `sevjala version`, `sevjala agent`, `sevjala service install|uninstall|start|stop|status`,
  `sevjala doctor` (four checks).
- Packages `cmd/`, `config/`, `logx/`, `localapi/`, `svc/`.
- Local API with one route, `GET /status`.
- Makefile, golangci-lint config, go-winres resources, go-win7 fetch + checksum, GoReleaser,
  a minimal GitHub Actions CI (D-009).

Out (later milestones): identity, discovery, the LAN listener (config carries `listen` but the
agent does not open it), firewall rules (M1, D-007), tray (M5), `test-integration` (M2),
`ui` target (M6), a release workflow (release stays local: `make release`).

## CLI contract

Exit codes everywhere: `0` success, `1` operational error, `2` usage error.

Global flag `--data-dir` (default: `SEVJALA_DATA_DIR` if set, else the OS default from the
blueprint: `%ProgramData%\Sevjala\`, `/Library/Application Support/Sevjala/`,
`/var/lib/sevjala/`).

| Command | Behaviour |
|---|---|
| `sevjala version` | Prints `sevjala <version> (<goos>/<goarch>, <go version>)`. Version comes from `-ldflags -X github.com/jufianto/sevjala/cmd.version=…`, default `dev`. |
| `sevjala agent [--role controller\|node]` | Runs the agent in the foreground (the service runs exactly this). Loads `config.toml`; `--role` overrides the file's role. No config and no `--role` → exit 1: `not installed: run 'sevjala service install --role controller\|node'`. |
| `sevjala service install --role controller\|node` | See Install below. `--role` is required (exit 2 if missing or invalid). |
| `sevjala service uninstall` | Stops and unregisters the service. Keeps the data dir and says where it is. Leaves the `sevjala` group. |
| `sevjala service start\|stop` | Service manager start/stop. |
| `sevjala service status` | See Status below. |
| `sevjala doctor` | Prints one line per check, `ok` or `FAIL` plus detail; exit 1 if any fails. Checks: data dir exists and is writable; `config.toml` parses and validates; service installed and running; local API answers `GET /status`. |

`install`, `uninstall`, `start`, `stop` need admin/root: Windows checks the process token is
elevated, Unix checks `euid == 0`. Otherwise exit 1 with `run this from an Administrator
prompt` / `run with sudo`.

### Install

1. Check privileges.
2. Create `<data-dir>/` and `<data-dir>/logs/`.
3. If `config.toml` is absent, write `role`, `listen = "0.0.0.0:17891"`,
   `discovery_port = 17890`. If present, leave it untouched; if its role differs from
   `--role`, exit 1 naming both roles (changing role is not P0's job).
4. Unix only: ensure group `sevjala` exists and add `$SUDO_USER` to it (Linux `groupadd
   --system` / `usermod -aG`; macOS `dseditgroup`). Print that the user must log out and in
   for the group to apply. No `$SUDO_USER` (real root login) → skip the add, say so.
5. Register the service via `kardianos/service`: name `sevjala`, display name
   `Sevjala Agent`, description `Sevjala — Database Backup & Replication agent`, arguments
   `agent --data-dir <abs data dir>`, system-level (Windows Service / LaunchDaemon with
   RunAtLoad + KeepAlive / systemd unit, restart on failure).
6. Start it and wait up to 10 s for the local API to answer. Success prints the same block
   as `service status`. No answer → exit 1 with the log file path.
7. Log `event=service_installed`.

### Status

```
service   running      (windows-service)
agent     answering    v0.1.0  role=node  up 2m13s  pid 4120
data dir  C:\ProgramData\Sevjala
```

`service` is `running`, `stopped` or `not installed` (from `Status()`, platform from
`Platform()`). `agent` is `answering …` or `not answering (<error>)`. Exit 0 only when the
service is running **and** the agent answers.

Privileges: `doctor` needs sudo / an Administrator prompt everywhere (it writes a probe file
into the data dir); on Windows `status` does too (the pipe ACL allows only Administrators +
SYSTEM). On macOS/Linux, members of `sevjala` reach the socket
without sudo. If the service-manager query itself is refused unprivileged (e.g. `launchctl`
not listing system daemons to a normal user), `service` shows `unknown (run with sudo for
the service state)`; the agent check still runs, and an answering agent counts as running
for the exit code.

## Runtime

**Agent boot:** load config → open logger → listen on the local endpoint → serve. On service
stop or SIGINT/SIGTERM: `http.Server.Shutdown` with a 5 s timeout, remove the Unix socket, log
`event=agent_stopped`, close the log. `kardianos/service` `Run()` handles both foreground and
service mode.

**Local endpoint** (`localapi`):
- Default: Windows `\\.\pipe\sevjala` via `go-winio` with SDDL `D:P(A;;GA;;;BA)(A;;GA;;;SY)`
  (Administrators + SYSTEM); Unix `/var/run/sevjala.sock`.
- When `--data-dir` is not the OS default (dev, tests), the endpoint moves with it: Unix
  `<data-dir>/sevjala.sock`, Windows `\\.\pipe\sevjala-<first 8 hex of sha256(abs data dir)>`.
  One knob, and two dev agents never collide. On Windows the custom pipe keeps the default
  DACL so an un-elevated developer can reach their own agent.
- Unix socket: remove a stale socket before listening; `chmod 0660`; `chown root:sevjala`
  when the group exists, else leave `0600` root-only and log a warning.
- `GET /status` → `200 {"version":"…","role":"node","pid":4120,"uptime_s":133,"data_dir":"…"}`.
  Anything else → 404 JSON `{"error":"not found"}`.
- `localapi.Client` dials the endpoint (pipe or socket) with an `http.Client` whose transport
  `DialContext` ignores the host; timeout 3 s. Used by `service status`, `install` and `doctor`.

**Config** (`config`): `BurntSushi/toml`; struct mirrors the blueprint's `config.toml`
(`role`, `listen`, `discovery_port`, `[controller.database]`, `[controller.staging]`,
`[node.store]`). Validation in P0: `role` ∈ {controller, node}; ports 1–65535. Unknown keys
are an error (`MetaData.Undecoded()`), naming the key.

**Logging** (`logx`): `slog.NewJSONHandler` over a rotating writer at `<data-dir>/logs/sevjala.log`.
Rotation: when the next write would pass 10 MiB, close the file, shift `.8→.9 … .1→.2`
(dropping `.9`), rename current to `.1`, open a fresh file: 10 files total. Close before
rename because Windows cannot rename an open file. Foreground mode tees to stderr. A write
error is never fatal to the agent. P0 events (`event` attribute): `agent_started`,
`agent_stopped`, `service_installed`, `service_uninstalled`. Names are provisional until the
PRD's section 25 list is in the repo.

## Build, release, CI

- **Version info / manifest:** `winres/winres.json` declares `RT_MANIFEST` (asInvoker) and
  `RT_VERSION`. `go-winres` is pinned with `go get -tool github.com/tc-hib/go-winres@v0.3.3`
  and run as `go tool go-winres make --product-version … --file-version …`, producing
  `rsrc_windows_{amd64,386}.syso` at the root (gitignored).
- **go-win7:** `scripts/fetch-go-win7.sh` downloads
  `go-for-win7-<host os>-<host arch>.zip` from release `patched-1.27.2`, verifies SHA-256
  against `scripts/go-win7.sha256` (committed; one line per host: darwin-arm64,
  darwin-amd64, linux-amd64, linux-arm64), and unpacks to `.cache/go-win7/`. Mismatch →
  exit 1. Idempotent: skips the download when the unpacked SDK's checksum stamp matches.
- **GoReleaser** (`.goreleaser.yaml`, v2): build `stock` (windows/amd64, darwin/amd64,
  darwin/arm64, linux/amd64) and build `win7` (windows/amd64, windows/386) with
  `tool: .cache/go-win7/go/bin/go` and `GOTOOLCHAIN=local`. All `CGO_ENABLED=0`, `-trimpath`,
  ldflags set the version. Before-hooks: fetch go-win7, run go-winres. Archives use format
  `binary` named as the blueprint's assets (`sevjala-windows-amd64.exe`,
  `sevjala-win7-amd64.exe`, `sevjala-win7-386.exe`, `sevjala-darwin-arm64`,
  `sevjala-darwin-amd64`, `sevjala-linux-amd64`), plus `checksums.txt`.
- **Makefile:** `build` (host binary `./sevjala`), `build-all` (`goreleaser release --snapshot
  --clean`: names the assets and writes `checksums.txt`, publishes nothing), `build-win7` (fetch + `goreleaser build --snapshot --clean --id win7`), `test`
  (`go test ./...`, adds `-race` on darwin/linux), `lint` (`golangci-lint run`), `release`
  (`goreleaser release --clean`). `build-win7` and `build-all` run on macOS/Linux hosts only.
- **golangci-lint:** `.golangci.yml` (v2) with the `standard` set plus `errorlint`,
  `misspell`, `gofmt` formatter.
- **CI** (`.github/workflows/ci.yml`, on push to `master` and PRs, docs-only changes skipped,
  stale runs cancelled): one ubuntu job, about 1–2 minutes — `go vet` for linux, darwin and
  windows, golangci-lint, `go test -race` (D-009). The build matrix runs locally with
  `make build-all`; service install is smoke-tested by hand (macOS locally, Windows 7 VM).
- **Windows 7 smoke** (manual, D-005): VM with Windows 7 SP1 + KB3125574 on the owner's
  Windows 11 PC. Copy `sevjala-win7-amd64.exe` (and `-386.exe` on a 32-bit VM); from an
  Administrator prompt run `version`, `service install --role node`, `service status`,
  `doctor`, `service stop`, `service status` (exit 1), `service start`, `service uninstall`.
  Paste the transcript into the PR. The PR stays open until it is in.

## Testing

- `config`: table-driven — valid controller/node files, missing role, bad role, bad port,
  unknown key (named in the error), defaults written by install round-trip.
- `logx`: rotation at the threshold with a small limit, keeps exactly 10 files, oldest
  dropped, writes after rotation land in the new file.
- `localapi`: loopback test — server on a temp socket (Unix) or unique pipe (Windows),
  client `GET /status` returns the fields; unknown path → 404; endpoint derivation for
  default vs custom data dir.
- `cmd`: status/doctor rendering from fixed inputs; `--role` validation exit codes.
- `svc`: install/uninstall is proven by the manual smokes (macOS, Windows 7 VM), not unit tests.
- TDD per package (superpowers:test-driven-development).

## Decisions recorded with this spec

D-004 … D-008 in [decisions.md](../../decisions.md). Open question added there: the tray
(M5) runs un-elevated under UAC and cannot open an Administrators+SYSTEM pipe.

## Docs touched

- `docs/protocol.md`: Local API `GET /status` (first implemented wire message).
- `docs/PROGRESS.md`: P0 → in progress, link this spec and the plan.
- `README.md`: install and status commands once they work.
