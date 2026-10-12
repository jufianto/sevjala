# Sevjala: instructions for Claude Code

These instructions apply throughout this repository. Claude Code is the primary agent;
`AGENTS.md` only points other agents here.

## Start here

1. Read `docs/PROGRESS.md` for the milestone in play, then that milestone's row in the
   Roadmap of `docs/BLUEPRINT.md` and the sections listed for it under "Agent handoff →
   Milestone → sections". Nothing outside that milestone's scope is in play.
2. `docs/BLUEPRINT.md` is the source of truth. Its Architecture decisions (AD-01..AD-17) are
   locked: never change one silently. Propose a new AD row with a reason and stop until the
   owner accepts it.
3. Anything the blueprint leaves unspecified: decide it, add a one-line row to
   `docs/decisions.md`, and raise it in the session summary.
4. Inspect the actual source before editing. Docs are a snapshot, not proof of the current
   code.

The owner's current request takes precedence over these repository defaults. When the owner
changes a durable preference, update the corresponding shared document in the same change. Do
not invent approval requirements or ask again about decisions already made.

## Product intent

- Owner and audience: @jufi; operators of small offices running MySQL/MariaDB (mostly XAMPP on
  Windows, down to Windows 7) with no database-admin knowledge.
- Purpose: LAN-first backup of one database server to several backup PCs: discover, pair,
  back up, verify, retain, restore. One Go binary, CLI and tray; browser UI in Phase 2.
- Priorities, in order: data safety, reliability, simple recovery, transparent status, minimal
  operator intervention.
- Scope boundaries: no cloud, no Internet. Sevjala never implements database replication
  itself. Replication monitoring, PITR, self-heal, notifications, encryption and PostgreSQL are
  Phase 3 (AD-17).

## Engineering boundaries

- Stack: Go, one module, one binary (`sevjala`); cobra CLI; HTTP/1.1 over mTLS on the LAN, HTTP
  over a named pipe / Unix socket locally; SQLite (`modernc.org/sqlite`) on the Controller only.
  Phase 2 adds Svelte 5 + Vite in `ui/`, embedded in the binary.
- Layout: packages live directly at the repo root, no `internal/` (`docs/decisions.md` D-001).
  Create a package dir only in the milestone that needs it.
- Pure Go and `CGO_ENABLED=0` for the agent, the CLI and every Windows asset. cgo only behind
  the `tray` build tag on macOS/Linux. Everything must also compile with the go-win7 SDK of
  the same minor: avoid packages that bind newer Windows APIs directly.
- Dependencies are the ones the blueprint names (cobra, kardianos/service, go-winio,
  modernc.org/sqlite, go-keyring, fyne.io/systray, BurntSushi/toml). Anything else needs a
  reason in `docs/decisions.md`.
- Go modules with the committed `go.sum`. Read `go.mod` and `.tool-versions` for the current
  Go version; do not assume versions from a previous session.
- Edit source, not generated output (`dist/`, `ui/dist/`, `ui/node_modules/`).
- Secrets (DB password, pairing code) live only in the OS keyring. They never appear in config
  files, logs, `/status`, test fixtures, or commits.
- Releases go through GoReleaser. A build request alone does not authorize a release.

## Non-negotiables

- Nodes never initiate LAN connections; Nodes never delete without an authenticated `DELETE`.
- No backup is reported `verified` before hash match and atomic rename.
- Keep the CLI usable without the tray or UI; they are clients, not requirements.
- Every milestone meets the blueprint's "Definition of done", including `docs/protocol.md`
  updated when a wire message or on-disk format changes.

## Commands and verify order

Package manager is **Go modules**. Until P0 adds the Makefile, use the Go commands directly.

| Command | What it does |
|---|---|
| `go build ./...` | Compile every package |
| `go vet ./...` | Static diagnostics |
| `golangci-lint run` | Lint (v2) |
| `gofmt -l .` | List unformatted files; scope `gofmt -w` to touched files |
| `go test ./...` | Unit tests (`-race` on macOS/Linux only) |
| `make build-all` / `make build-win7` | Release matrix incl. go-win7 assets (from P0) |
| `make test-integration` | Two in-process agents on loopback + MariaDB container, needs Docker (from P0/M2) |

Verify order after edits: `go vet ./...` → `golangci-lint run` → `go test ./...` →
`make build-all` before a milestone PR.

## Stack quirks

- Windows 7 builds use the XTLS/go-win7 patched SDK, downloaded by a pinned URL and checked
  against a SHA-256 recorded in the repo (`scripts/`). `-race` is unsupported there.
- `//go:embed` cannot reach a parent directory: the Svelte build is embedded by `ui/embed.go`
  and served by `uiproxy/` (D-001).
- Logs are `log/slog`, structured, with an `event` field per event in the PRD's event list.
  Errors wrap with `%w`. No global state beyond the service entry point.
- Tests are table-driven; anything touching the LAN or local API also gets a loopback
  integration test.

## Claude Code workflow

- One milestone per session, in order. Start with `/work-from-issue` (an issue number, or no
  argument for the next row of `docs/PROGRESS.md`). It runs the superpowers flow:
  brainstorming → spec in `docs/superpowers/specs/` → writing-plans → plan in
  `docs/superpowers/plans/` → owner approval → executing-plans or subagent-driven-development →
  requesting-code-review → verification-before-completion → finishing-a-development-branch.
- No code before the owner approves the plan. A milestone is architectural work: the spec and
  plan are reviewed, not skipped.
- Use `superpowers:test-driven-development` for every package; use
  `superpowers:systematic-debugging` before proposing a fix for a failing test.
- Use `product-dev` only for Phase 3 ADRs or an AD change proposal.
- Branch per milestone (`p0-foundation`, `m1-coordination`, …); never commit to `master`
  directly once P0 starts. Commit or push only when the owner asks.

### Tools

- **gopls LSP** (`gopls-lsp` plugin): go-to-definition, references and diagnostics; prefer it
  over grep for Go symbols.
- **context7**: current docs for cobra, kardianos/service, go-winio, modernc.org/sqlite,
  go-keyring, fyne.io/systray, BurntSushi/toml, and later Svelte 5 / Vite. Use it before
  writing against any of their APIs.
- **codebase-memory (cbm-personal store)**: once P0 has code, index this repo and use
  `search_graph` / `trace_path` for call chains across packages.
- **Playwright MCP** (Phase 2 only): verify the `sevjala ui` dashboard in a real browser.
- Shell commands are rewritten through `rtk` by the global hook; use `rtk proxy <cmd>` when
  raw output is needed (e.g. a failing test's full log).

## Working and finishing
- Make the requested change directly and preserve unrelated work.
- For code changes, run the verify order above. For documentation-only changes, check
  references and consistency.
- Update `docs/PROGRESS.md` and `docs/decisions.md` when a milestone or decision changes.
- Finish with what changed, what was checked, and any material unfinished work, including the
  exit-criterion transcript for a milestone. Say plainly which manual smokes (real Windows 7
  box, multiple PCs) remain undone.

## Skill parameters

| Parameter | Value |
|---|---|
| Tracker | GitHub `jufianto/sevjala` via `gh` |
| Progress file | `docs/PROGRESS.md` |
| Docs index | `docs/README.md` |
| Test command | `go test ./...` (later `make test`) |
| PRD | `docs/BLUEPRINT.md` (no separate PRD dir) |
| ADR dir | `docs/adr/`, `NNNN-<slug>.md` |
| Functional spec dir | `docs/superpowers/specs/` |
| Reference docs | `docs/BLUEPRINT.md`, `docs/protocol.md` |
| Discipline skills | `go-conventions` once P0 code exists; until then the stack is Go stdlib + the blueprint's libraries |

## Delegation and model routing

- When the runtime supports subagents and independent work would save time or improve quality,
  delegate bounded, non-overlapping tasks. The coordinating agent stays responsible for
  decisions, integration, and the final result.
- Do not delegate for ceremony. Never assign overlapping edits to agents sharing a checkout.

| Tier | Use for | Claude |
|---|---|---|
| Mechanical | Inventories, narrow research, isolated fixes | haiku |
| Scoped | One scoped implementation or focused debugging task | sonnet |
| Complex | Multi-file implementation, difficult integration | opus |
| Architecture | Architecture, AD proposals, final integration and review | opus |

# Engineering defaults

## Write the least code that works

- Before writing a helper, type, or pattern, search the repo for one that already does it and
  reuse it. Re-implementing what exists a few files over is the most common slop.
- Prefer, in order: the standard library, a native platform feature (a database constraint over
  application code), a dependency already in the manifest. Never add a dependency for a few
  lines of code.
- No speculative code: no interface with one implementation, no config for a value that never
  changes, no scaffolding "for later", no flag or branch for a case nobody asked for.
- A bug report names a symptom. Grep every caller of the function before editing, and put the
  fix at the chokepoint all callers route through, not only on the path the report describes.
- Never trade away validation at trust boundaries, error handling, security, or an existing
  layering rule to make a diff smaller. The smallest change in the wrong layer is a second bug.

## Tool choice

- Do not use `python3 -c "..."` to read files, validate JSON, or search. Use the Read tool for
  file contents, `jq` for JSON, and `rg` / `grep` for searching.

## Comments

- Default is no comment. Write one only when the logic is genuinely complex or the why cannot be
  seen from the code, and keep it to one or two lines.
- Never narrate a change, where code moved from, or a migration. Long reasoning goes in the
  commit body, the PR, or `docs/`, not the source. This applies to tests too.
- No ticket or issue numbers in code comments; cite a stable blueprint section instead. The
  ticket reference belongs in the commit message.
