# Decisions

Choices the [blueprint](./BLUEPRINT.md) leaves open, one line each (Agent handoff, rule 4).
Changing an AD-xx is not done here: propose a new AD row in the blueprint and stop.
Hard-to-reverse choices beyond the blueprint get an [ADR](./adr/README.md).

| ID | Date | Decision | Why |
|---|---|---|---|
| D-001 | 2026-10-12 | Flat layout: `main.go` at the root calls `cmd.Execute()`; one package per component at the repo root, no `internal/`; the Go UI proxy package is `uiproxy/`; `ui/` holds the Svelte project plus `embed.go` exposing `dist/`. Package dirs are created by the milestone that needs them. | Owner preference. `internal/` only blocks imports from other modules, which an app binary never has. `//go:embed` cannot reach a parent dir, so the embed lives in `ui/`, and the Go package needs a name that does not clash with it. |
| D-002 | 2026-10-12 | Docs: `BLUEPRINT.md` is the PRD and architecture in one; no separate PRD or backfilled ADRs for AD-01..17. `docs/adr/` is for AD changes and Phase 3 items; milestone designs and plans go in `docs/superpowers/specs` and `plans`. | One source of truth; copies drift. The blueprint already says Phase 3 items ship as ADRs. |
| D-003 | 2026-10-12 | Module `github.com/jufianto/sevjala`, default branch `master`, `go.mod` at `go 1.27.0`, dev toolchain pinned to Go 1.27.2 in `.tool-versions` (asdf). | Matches batlog; `1.27.0` keeps the floor at the minor go-win7 must match. |
| D-004 | 2026-10-12 | Win7 assets are a GoReleaser build with `tool:` pointing at the go-win7 SDK (`patched-1.27.2`), not a post-hook. | Hook-made files skip GoReleaser's checksums and upload; one run yields all six assets. |
| D-005 | 2026-10-12 | Win7 smoke is manual on a Windows 7 SP1 + KB3125574 VM (on a Windows 11 host), transcript pasted in the PR; no `smoke-win7` CI job. | No Win7 CI runner exists; the owner has the VM. |
| D-006 | 2026-10-12 | `logx` uses its own size-based rotating writer (10 MiB × 10 files), no lumberjack. | The requirement is size × count only; keeps the dependency list to the blueprint's. |
| D-007 | 2026-10-12 | Windows firewall rules for 17890/udp and 17891/tcp are added by `service install` from M1, not P0. | P0 opens no LAN port. |
| D-008 | 2026-10-12 | `--data-dir` / `SEVJALA_DATA_DIR` overrides the data dir; a non-default data dir also moves the local endpoint (`<data-dir>/sevjala.sock`, or `\\.\pipe\sevjala-<hash>`). `service install` creates group `sevjala` and adds `$SUDO_USER` on macOS/Linux. | Dev and tests run without root and never collide; the CLI works without sudo after install. On Windows a custom pipe keeps the default DACL so an un-elevated developer reaches their own agent. |
| D-009 | 2026-10-12 | CI is one ubuntu job (~1–2 min): `go vet` for linux/darwin/windows, golangci-lint, `go test -race`. Windows/macOS test runners, the GoReleaser build and the 3-OS service smoke are out until the owner reviews any job over ~3 min. | Owner wants CI small and fast; cross-OS `go vet` still compiles every OS-specific file. Windows pipe tests now run only on a Windows machine. |
| D-010 | 2026-10-12 | Data dir lockdown moves into M1 (first private key): Unix root-owned 0700, Windows owner Administrators + protected DACL (SYSTEM, Administrators full); install refuses a dir it does not trust, or a symlink. | Closes the M2 hard gate early: a non-admin user must not read `identity/key.pem` or plant `config.toml`. |
| D-011 | 2026-10-12 | The pairing code lives only in the node agent's memory (no keyring in M1); an agent restart clears it. | Never on disk; the keyring arrives with the DB password in M2. Restart mid-pairing costs one `sevjala code`. |
| D-012 | 2026-10-12 | While a code is active a node accepts any client cert, for `/identity` and `/pair` only; a successful pair replaces the trusted controller. With no active code, unknown certs fail the TLS handshake. | Re-pairing after a controller reinstall needs only a new code on the node; no unpair command. |
| D-013 | 2026-10-12 | LAN `GET /status` in M1 returns `agent_id, role, version, uptime_s, health, backups:[], problems:[]`; `store` arrives in M2. Local `/status` adds `agent_id`, `fp`. | Only what M1 can fill truthfully. |
| D-014 | 2026-10-12 | CLI `<agent>` = exact agent_id, else hostname (any case), else a unique agent_id prefix of at least 4 chars; ambiguity lists the candidates. | Nobody types a UUID; never guess between two PCs. |
| D-015 | 2026-10-12 | Discovery results are cached in controller memory; `pair` needs a `discover` or `agents add` in the same agent run. Discover refreshes hostname/address of a paired agent whose fp matches. | Pairing must pin the fp seen before the code is sent; DHCP changes heal on the next discover. |
| D-016 | 2026-10-12 | Role logic lives in package `coord` behind `localapi.Backend` (not in the blueprint's package list). | Keeps `localapi` free of LAN code and the role switch in one place. |
| D-017 | 2026-10-12 | Windows firewall rules `Sevjala-LAN-API` (TCP) and `Sevjala-Discovery` (UDP): port-scoped, inbound allow, all profiles; install recreates them, uninstall removes them. | Rule names without spaces survive `netsh` quoting; port scope survives moving the binary. |

## Open questions

From the blueprint (settle during P0/M1), plus gaps found while preparing the repo.

- [ ] Exact MariaDB/MySQL versions bundled with the XAMPP releases in use, to pin `mysqldump` flag compatibility
- [ ] Whether SERVER-01 has KB3125574, which decides go-win7 1.27 vs 1.26.x for the Win7 assets
- [ ] Staging `keep` default (12) vs available disk on the Controller
- [ ] Whether a node may serve two controllers in v2 (affects `peers.json` shape now)
- [x] **Before M2 (hard gate):** lock down the data dir (done in M1, D-010). On Windows `MkdirAll` under `%ProgramData%` inherits an ACL that lets Users create files, and a user can pre-create `C:\ProgramData\Sevjala\` with their own `config.toml`; once M2 makes SYSTEM run `controller.database.dumper`, that is a local privilege escalation. Install must set a protected DACL (SYSTEM + Administrators full, Users read) owned by Administrators, refuse a pre-existing dir not owned by Administrators/SYSTEM, and on Unix require root ownership and no group/world write.
- [ ] M5: under UAC the tray runs un-elevated even for an admin user, so it cannot open a pipe ACL'd to Administrators + SYSTEM. Needs a read-only ACE for interactive users or a second pipe before M5.
- [ ] The blueprint cites a PRD ("design principle kept from the PRD", "every event in PRD section 25"). That PRD is not in the repo; the section 25 event list is needed before M1's definition of done can be met.
