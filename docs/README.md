# Sevjala — documentation

| File | Contents |
|---|---|
| [BLUEPRINT.md](./BLUEPRINT.md) | Source of truth: product, architecture decisions (AD-xx), protocol, data model, security, toolchain, roadmap, agent handoff |
| [PROGRESS.md](./PROGRESS.md) | Milestone status; the next milestone to build |
| [decisions.md](./decisions.md) | One-line decisions the blueprint leaves open, plus open questions |
| [protocol.md](./protocol.md) | Wire messages and on-disk formats as implemented |
| [smoke-tests.md](./smoke-tests.md) | Manual tests CI cannot run (service install, Win7 VM, multi-PC), per milestone |
| [test-walkthrough.md](./test-walkthrough.md) | Step-by-step run on one Windows PC (controller) and one Mac (node), LAN or Tailscale |
| [restore-guide.md](./restore-guide.md) | For operators: get a database back (new database, replace, controller PC lost) |
| [adr/](./adr/) | ADRs: AD changes and Phase 3 items only |
| `superpowers/specs/`, `superpowers/plans/` | Per-milestone design and implementation plan |
| [blueprint-v1.pdf](./blueprint-v1.pdf) | Original blueprint export |

**Reading order for a session:** PROGRESS.md (which milestone) → the milestone's row in
the Roadmap → the sections listed for it under Agent handoff → decisions.md.
