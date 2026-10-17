# Progress

One row per milestone from the [Roadmap](./BLUEPRINT.md#roadmap), built in order. Update
the row when a milestone's PR merges, and set the next one to `next`.

| Milestone | Status | Spec / plan | PR |
|---|---|---|---|
| P0 Foundation | code done — manual smokes pending ([how to test](./smoke-tests.md)) | [spec](./superpowers/specs/2026-10-12-p0-foundation-design.md) · [plan](./superpowers/plans/2026-10-12-p0-foundation.md) | |
| M1 Coordination | code done — two-PC smoke pending ([how to test](./smoke-tests.md)) | [spec](./superpowers/specs/2026-10-12-m1-coordination-design.md) · [plan](./superpowers/plans/2026-10-12-m1-coordination.md) | |
| M2 First backup | code done — 3-PC smoke pending ([how to test](./smoke-tests.md)) | [spec](./superpowers/specs/2026-10-13-m2-first-backup-design.md) · [plan](./superpowers/plans/2026-10-13-m2-first-backup.md) | |
| M3 Unattended | code done — 48 h smoke pending ([how to test](./smoke-tests.md)) | [spec](./superpowers/specs/2026-10-13-m3-unattended-design.md) · no written plan (owner waived it) | |
| M4 Restore | code done — controller smokes passed on the real PC (restore, replace, failed replace, file, CHECKSUM TABLE equal); node smokes passed with one node (fetch from it, damaged copy refused then replaced, restore on a node refused); a second node and Ctrl-C still undone ([how to test](./smoke-tests.md)) | [spec](./superpowers/specs/2026-10-16-m4-restore-design.md) · [plan](./superpowers/plans/2026-10-16-m4-restore.md) | |
| M5 Operator comfort | code done — smokes pending: daily commands without admin, UAC prompts, tray colours, tray at login, update/uninstall with the tray, Windows 7 tray ([how to test](./smoke-tests.md)) | [spec](./superpowers/specs/2026-10-17-m5-operator-comfort-design.md) · no written plan (owner waived it) | |
| M6 UI shell | | | |
| M7 UI screens | | | |

Phase 3, waiting until after M7: [ADR 0001 replication](./adr/0001-replication-setup-and-monitor.md) (proposed; the owner chose to keep the blueprint order).
