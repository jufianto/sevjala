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
| M5 Operator comfort | code done — passed on the Windows PC as a normal user: `status`, the tray at login and from the Start menu, Open status, the tray restarting after an update; still pending: UAC prompts, tray colours other than green, uninstall with the tray, Windows 7 tray ([how to test](./smoke-tests.md)) | [spec](./superpowers/specs/2026-10-17-m5-operator-comfort-design.md) · no written plan (owner waived it) | |
| M6 UI shell | code done — checked in a real browser against a local controller + 2 backup PCs (live colours, offline PC, Back up now, two dashboards, dark mode, phone width); smokes on the Windows PC and a Windows 7 browser pending ([how to test](./smoke-tests.md)) | no written spec or plan (owner went straight to code, D-078) | |
| M7 UI screens | code done — Backups, Restore, Backup PCs (find, pair, check, remove), Schedules, Database and Logs screens on the local API; admin only for the dump program and restore from a file (AD-20 amended), typed confirmation for changes that delete backups, `by` in the log; checked in a real browser against a local controller + 2 backup PCs; smokes on the Windows PC and a Windows 7 browser pending ([how to test](./smoke-tests.md)) | [spec](./superpowers/specs/2026-10-17-m7-ui-screens-design.md) · [plan](./superpowers/plans/2026-10-17-m7-ui-screens.md) | |
| Managers (owner request, after M7) | next — plan awaiting approval: an admin on the controller marks a paired backup PC as manager; from it `sevjala --controller …` and `sevjala ui --controller` run on the controller over links the controller opens (AD-21, nodes still never dial) | [spec](./superpowers/specs/2026-10-17-managers-design.md) · [plan](./superpowers/plans/2026-10-17-managers.md) | |

Phase 3, waiting until after M7: [ADR 0001 replication](./adr/0001-replication-setup-and-monitor.md) (proposed; the owner chose to keep the blueprint order).
