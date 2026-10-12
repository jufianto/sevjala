# Decisions

Choices the [blueprint](./BLUEPRINT.md) leaves open, one line each (Agent handoff, rule 4).
Changing an AD-xx is not done here: propose a new AD row in the blueprint and stop.
Hard-to-reverse choices beyond the blueprint get an [ADR](./adr/README.md).

| ID | Date | Decision | Why |
|---|---|---|---|
| D-001 | 2026-10-12 | Flat layout: `main.go` at the root calls `cmd.Execute()`; one package per component at the repo root, no `internal/`; the Go UI proxy package is `uiproxy/`; `ui/` holds the Svelte project plus `embed.go` exposing `dist/`. Package dirs are created by the milestone that needs them. | Owner preference. `internal/` only blocks imports from other modules, which an app binary never has. `//go:embed` cannot reach a parent dir, so the embed lives in `ui/`, and the Go package needs a name that does not clash with it. |
| D-002 | 2026-10-12 | Docs: `BLUEPRINT.md` is the PRD and architecture in one; no separate PRD or backfilled ADRs for AD-01..17. `docs/adr/` is for AD changes and Phase 3 items; milestone designs and plans go in `docs/superpowers/specs` and `plans`. | One source of truth; copies drift. The blueprint already says Phase 3 items ship as ADRs. |
| D-003 | 2026-10-12 | Module `github.com/jufianto/sevjala`, default branch `master`, `go.mod` at `go 1.27.0`, dev toolchain pinned to Go 1.27.2 in `.tool-versions` (asdf). | Matches batlog; `1.27.0` keeps the floor at the minor go-win7 must match. |

## Open questions

From the blueprint (settle during P0/M1), plus gaps found while preparing the repo.

- [ ] Exact MariaDB/MySQL versions bundled with the XAMPP releases in use, to pin `mysqldump` flag compatibility
- [ ] Whether SERVER-01 has KB3125574, which decides go-win7 1.27 vs 1.26.x for the Win7 assets
- [ ] Staging `keep` default (12) vs available disk on the Controller
- [ ] Whether a node may serve two controllers in v2 (affects `peers.json` shape now)
- [ ] The blueprint cites a PRD ("design principle kept from the PRD", "every event in PRD section 25"). That PRD is not in the repo; the section 25 event list is needed before M1's definition of done can be met.
