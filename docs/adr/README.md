# Architecture decision records

The 17 locked decisions (AD-01..AD-17) live in the
[blueprint](../BLUEPRINT.md#architecture-decisions) and are not copied here.

An ADR is written for:
- a proposed change to an AD-xx (stop work until it is accepted, then add the new AD row
  to the blueprint and link this ADR from it);
- each Phase 3 item (replication monitor, PITR, self-heal, notifications, resumable
  transfer, file encryption, PostgreSQL), as the roadmap requires.

One decision per file, `NNNN-<slug>.md`, under one page: Title · Status · Date · Context ·
Options · Decision · Consequences (at least two real downsides). ADRs are append-only:
to change one, write a new ADR that supersedes it and link both ways. Easily reversible
choices go in [decisions.md](../decisions.md) instead.

| # | Decision | Status |
|---|---|---|
| [0001](./0001-replication-setup-and-monitor.md) | Replication: Sevjala sets up and watches MariaDB's own replication (primary → read-only replica) | Proposed, deferred to Phase 3 |
