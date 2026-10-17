# M7 UI Screens Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every CLI operation on the controller can be done from `sevjala ui` by a normal user, with
typed confirmation, a "by" account in the log and a last-good-copy guard replacing most admin checks.

**Architecture:** Server first (caller account, logs route, last-good rule, `confirm_required`,
AD-20 shrink), then the CLI follows the new 409, then the Svelte screens, which call only `/api/*`
through the M6 proxy. Pure UI logic lives in `ui/src/lib/*.js` with `node --test`.

**Tech Stack:** Go (stdlib, cobra, x/sys/windows, modernc sqlite), Svelte 5 + Vite (no new deps).

**Spec:** [docs/superpowers/specs/2026-10-17-m7-ui-screens-design.md](../specs/2026-10-17-m7-ui-screens-design.md)

## Global Constraints

- AD-01..AD-19 unchanged; AD-20 amended exactly as the spec's quoted row (owner approved 2026-10-17).
- Pure Go, `CGO_ENABLED=0`, go-win7 compatible; no new Go or npm dependencies.
- Admin stays required only for: setup, update, uninstall, `service *`, firewall/start-at-login/Start
  menu, `PUT /db` with `dumper`, `POST /restore` with `file`.
- Confirmation words: replace live → the database name; policy change/add that deletes → the
  policy name; forced PC removal → the PC's hostname. The 409 body is
  `{"error":"…","code":"confirm_required","word":"<word>","deletes":N,"hosts":["…"]}`.
- The database password never reaches the browser, a log line, an error or `GET /db`.
- Screens call only `/api/*` and `/events`; hash routing; `build.target` chrome109/firefox115.
- Every screen updates from `/events` or polls its own `GET` every 10 s while visible; phone width
  and dark mode as in M6; every failed action shows the agent's own message.
- Operator text says "backup PC", "schedule" (UI) / "policy" (CLI, as today), "the controller's own
  copy", never "staging"; no ticket numbers in code comments.
- Tests table-driven; local API changes get a loopback test; `go vet`/`golangci-lint` for host,
  windows, windows/386, linux and `-tags tray` stay clean.
- Commits on master, one or more per task, ending with the `Claude-Session:` line; no push.

## Review Focus

1. A PC offline while keep is lowered: its copies are not in the last poll, so a dry run built only
   from polls would ask for no confirmation and delete later. Expected: the preview counts store
   copies of unpolled PCs, so the prompt still appears (test in Task 4).
2. A confirm word that matches but the plan grew between prompt and resend (another change landed).
   Expected: the server re-checks on the confirmed request; a confirm only skips the prompt, never
   the last-good rule (tests in Tasks 3 and 5).
3. Log files with a half-written last line, a non-JSON line, or a missing `.3` rotation between
   `.2` and `.4`. Expected: skipped silently, the rest returned newest first (test in Task 2).
4. A caller whose account name cannot be looked up (deleted Unix user, SID lookup failure).
   Expected: `by` is the numeric uid or the SID string, never empty, never refused (test in Task 1).
5. The browser export of a backup the controller no longer holds in its own copy (unstaged between
   list and click). Expected: the 404 message shows in the page, no empty file is saved (Task 9:
   Export re-reads `GET /held` first and is offered only for ids it lists).

---

### Task 1: Caller account and the `by` attribute

**Files:**
- Modify: `logx/logx.go`, `logx/logx_test.go`
- Modify: `localapi/caller.go`, `localapi/server.go`, `localapi/endpoint_windows.go`,
  `localapi/endpoint_unix.go`, `localapi/endpoint_unix_test.go`, `localapi/endpoint_windows_test.go`
- Modify: `coord/coord.go`, `coord/controller.go`, `coord/policy.go`, `coord/database.go`,
  `coord/backup.go`, `coord/restore.go`, `coord/reconcile.go`, `coord/held.go`; test `coord/by_test.go`

**Interfaces:**
- Produces: `logx.WithBy(ctx context.Context, account string) context.Context`,
  `logx.By(ctx context.Context) string`, `logx.JSON(w io.Writer) *slog.Logger` (the handler `Open`
  uses: JSON lines plus `by` from the context when set), `logx.EventBackupExported = "backup_exported"`.
- Produces: `localapi.CallerAccount(ctx) string` (= `logx.By`); unexported
  `caller(c net.Conn) (admin bool, account string)` replaces `callerAdmin` on both platforms.

- [ ] **Step 1: Write the failing tests**
  - `logx.TestByFromContext`: `l := JSON(&buf)`; `l.InfoContext(WithBy(ctx, `OFFICE\ana`), "x", Event("policy_changed"))`
    → record `by == "OFFICE\\ana"`; `l.Info("y")` → no `by` key.
  - `localapi.TestCallerAccountOverSocket` (unix, real socket via existing endpoint test setup): a
    request's `CallerAccount(ctx)` equals `user.Current().Username`.
  - `localapi.TestCallerAccountOverPipe` (windows): equals `DOMAIN\user` from the test process token
    (`windows.GetCurrentProcessToken().GetTokenUser()` + `LookupAccount("")`).
  - `localapi.TestAccountFallback` (table, pure): `accountName(uid=4242 unknown) == "4242"`; Windows
    `sidName(sid with failing lookup) == sid.String()`.
  - `coord.TestChangesLogWhoDidIt`: controller logging through `logx.JSON(&buf)`; with
    `ctx := logx.WithBy(asUser, "tester")` call `AddPolicy`, `SetPolicy`, `RemovePolicy`,
    `BackupNow(ctx, db, nil)`, `RunRetention(ctx, false)`; each change line (`policy_changed`,
    `backup_started`, …) carries `"by":"tester"`; a scheduled backup (`ctrl.fire`/existing schedule
    test path) has no `by`.
- [ ] **Step 2: Run** `go test ./logx ./localapi ./coord -run 'By|Account|WhoDidIt'` — Expected: FAIL
  (undefined `WithBy`, `CallerAccount`).
- [ ] **Step 3: Implement.** `logx`: a small `slog.Handler` wrapper whose `Handle(ctx, r)` adds
  `slog.String("by", v)` when `By(ctx) != ""`; `Open` uses it. `localapi`: Windows reads the token user
  once in `caller` (`u.User.Sid.LookupAccount("")` → `domain + "\\" + account`, else `Sid.String()`);
  Unix `user.LookupId(strconv.Itoa(uid))` → `Username`, else the uid. `ConnContext` sets
  `logx.WithBy(WithAdmin(ctx, admin), account)`. coord: the events of pair, agent add/remove, policy
  add/change/remove, database set, backup now (`backup_started` for a request), restore started,
  manual retention deletes, and a new `backup_exported` in `OpenHeld` use `c.Log.InfoContext(ctx, …)`
  (or `WarnContext`) with the request context; async work keeps the values via
  `context.WithoutCancel(ctx)`.
- [ ] **Step 4: Run** the same command — Expected: PASS; then `go test ./...` PASS; cross-compile
  `GOOS=windows GOARCH=amd64 go test -c ./localapi` and run `TestCallerAccountOverPipe` on the
  Windows test PC — Expected: PASS.
- [ ] **Step 5: Commit** `feat: the local API knows the caller's account; change events log "by"`.

### Task 2: `GET /logs`

**Files:**
- Create: `logx/tail.go`, `logx/tail_test.go`
- Modify: `localapi/api.go` (Backend, `logList`), `localapi/server.go`, `localapi/client.go`,
  `localapi/localapi_test.go` (fake backend), `coord/coord.go` (`Service.LogDir string`, `Logs`),
  `svc/agent.go` (sets `LogDir: config.LogDir(a.DataDir)`)

**Interfaces:**
- Produces: `logx.Tail(dir string, limit int) ([]json.RawMessage, error)` — lines of
  `sevjala.log`, `.1` … `.{Keep-1}`, newest first, at most `limit`; non-JSON lines skipped; missing
  files skipped.
- Produces: `Backend.Logs(ctx context.Context, limit int) ([]json.RawMessage, error)`;
  route `GET /logs?limit=N` (default 500, 400 when not an integer in 1..2000) answering
  `{"entries":[…]}`; `Client.Logs(ctx, limit int) ([]json.RawMessage, error)`. Both roles, no admin.

- [ ] **Step 1: Write the failing tests**
  - `TestTailNewestFirstAcrossRotations`: `.2` has e1,e2; `.1` e3; current e4,e5 → `Tail(dir, 4)`
    gives e5,e4,e3,e2.
  - `TestTailSkipsBrokenLines`: a non-JSON line and a half-written last line are absent; a gap
    (`.1` and `.3`, no `.2`) still reads `.3`.
  - `TestTailEmptyDir` → empty slice, nil error.
  - `localapi.TestLogsRoute` (table): `?limit=abc` and `?limit=0` and `?limit=2001` → 400;
    no limit → backend called with 500; body has `entries`.
- [ ] **Step 2: Run** `go test ./logx ./localapi -run 'Tail|Logs'` — Expected: FAIL (undefined).
- [ ] **Step 3: Implement** reading each file whole (≤ `MaxBytes`), splitting lines, walking from
  the end, `json.Valid` per line; stop at `limit`.
- [ ] **Step 4: Run** — Expected: PASS; `go test ./...` PASS.
- [ ] **Step 5: Commit** `feat: GET /logs returns the newest log entries`.

### Task 3: Last good copy in retention

**Files:**
- Modify: `retention/retention.go`, `retention/retention_test.go`, `coord/reconcile.go`
  (`Input.Databases` from `c.config().Controller.Database.Databases`)

**Interfaces:**
- Produces: `retention.Input.Databases []string`; `retention.ReasonLastGood = "the newest verified
  backup of its database: kept whatever the policies say"`.

Rule: for each configured database, the newest non-expired backup with a verified copy on some node
(inventory-verified on a polled node, store state `verified` on an unpolled one) is never deleted
from a node that holds it verified; such a line goes to `Kept` with `ReasonLastGood`.

- [ ] **Step 1: Write the failing tests** (table rows in `retention_test.go`)
  - "dropping a destination while the wanted backup is verified nowhere else": policy `daily`
    Dest `[PC-2]`, PC-1 holds B1 (newest of `shop`) verified, PC-2 holds nothing, B1 staged →
    no delete of B1 on PC-1; `Kept` has `{B1, PC-1, ReasonLastGood}`; older B0 on PC-1 still deleted.
  - "keep lowered keeps the newest verified even when newer ones are unverified": keep 1, PC-1 holds
    B1 verified, B2 newer pending on PC-1 → B1 kept (already true via the same-node rule; asserts the
    reason stays the policy's, not last-good — guards against double reasons).
  - "a database no longer configured gets no guard": `Databases: nil` → B1 deleted as before.
  - "the last good copy on an offline node counts": PC-2 unpolled with store-verified B2 newer than
    B1 on PC-1 → B1 on PC-1 may be deleted per policy (B2 is the last good one).
- [ ] **Step 2: Run** `go test ./retention` — Expected: FAIL on the first row.
- [ ] **Step 3: Implement** `func (c *computer) lastGood() map[string]bool` (memoized, computed in
  `Compute` before nodes) and check it in `node()` before `c.delete` for the `!needed[id]` case.
- [ ] **Step 4: Run** `go test ./retention ./coord` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(retention): never delete a database's last verified backup`.

### Task 4: `confirm_required` and policy previews

**Files:**
- Modify: `localapi/api.go` (`Error.Word`, `Error.Deletes`, `Error.Hosts`; `PolicyChange.Confirm`,
  `PolicyChange.DryRun`; `Policy.Deletes`), `localapi/caller.go` or new `localapi/confirm.go`,
  `localapi/server.go` (`reply` writes the fields), `localapi/client.go` (`replyError` reads them)
- Modify: `coord/admin.go` (replace `policyDeletes`/`tagInUse` with the preview),
  `coord/policy.go`, `coord/restore.go`; tests `coord/confirm_test.go`, `localapi/localapi_test.go`

**Interfaces:**
- Produces: `localapi.ConfirmRequired(msg, word string, deletes int, hosts []string) error` (409,
  Reason `confirm_required`); `localapi.AsConfirm(err error) (*Error, bool)`.
- Produces: `PolicyChange{…, Confirm string "confirm,omitempty", DryRun bool "dry_run,omitempty"}`;
  `Policy.Deletes []RetentionAction "deletes,omitempty"` (filled only on a dry run).
- Produces: `(c *Controller) previewDeletes(ctx, next []store.Policy) ([]localapi.RetentionAction, error)`
  — deletes in `retention.Compute` with `next` that the current plan does not already hold; nodes
  not polled are treated as polled now holding their store-verified copies.

Behaviour: `AddPolicy`/`SetPolicy` with `DryRun` save nothing and answer the policy as it would be
plus `Deletes`. Without `DryRun`: when the preview deletes ≥ 1 and `Confirm != name` →
`ConfirmRequired("this change deletes N backups on PC-1, PC-2", name, N, hosts)`; with the right
word it saves. `Restore` with `Replace` and a wrong/missing `Confirm` answers
`ConfirmRequired("not confirmed: type the database name, X, to replace it", X, 0, nil)`.

- [ ] **Step 1: Write the failing tests**
  - `localapi.TestConfirmRequiredRoundTrip`: server replies `ConfirmRequired("m","daily",3,["PC-1"])`;
    client error: `AsConfirm` ok, `Code 409`, `Word "daily"`, `Deletes 3`, `Hosts ["PC-1"]`.
  - `coord.TestPolicyChangeNeedsConfirmWhenItDeletes` (table): keep 7→3 with 5 verified on PC-1 →
    409 word `hourly`, deletes 2; same with `Confirm:"hourly"` → saved; keep raised → no confirm;
    dry run → 200, `Deletes` len 2, policy unchanged in store; `AddPolicy` over a used tag with
    keep 1 → 409 word = that name; new tag → saved.
  - `coord.TestPreviewCountsOfflinePCs`: PC-1 marked offline (no poll), store says 5 verified →
    keep lowered still 409 with deletes 2.
  - `coord.TestConfirmDoesNotBypassLastGood`: confirmed change whose plan would delete the last
    good backup → saved, and the reconcile that follows keeps it (`ReasonLastGood`).
  - `coord.TestReplaceNeedsConfirmCode`: replace without confirm → `AsConfirm` ok, word = db.
- [ ] **Step 2: Run** `go test ./localapi ./coord -run 'Confirm|Preview|Replace'` — Expected: FAIL.
- [ ] **Step 3: Implement** as above; `hosts` sorted, unique, hostnames from the store.
- [ ] **Step 4: Run** — Expected: PASS; `go test ./...` PASS (the admin table still passes: admin
  checks are unchanged until Task 6).
- [ ] **Step 5: Commit** `feat: destructive policy changes and replace answer confirm_required`.

### Task 5: Forced removal: confirmation and last good copy

**Files:**
- Modify: `localapi/api.go` (Backend `RemoveAgent(ctx, agent string, force bool, confirm string)`),
  `localapi/server.go` (query `confirm`), `localapi/client.go`, `coord/coord.go`,
  `coord/controller.go`; test `coord/remove_test.go`

**Interfaces:**
- Produces: `Client.RemoveAgent(ctx, agent string, force bool, confirm string) (Agent, error)`.
- Produces: `func lastCopies(bs []store.Backup, agent string, staged func(string) bool, dbs []string) []string`
  in `coord/controller.go` — configured databases for which `agent` holds a verified copy and no
  other agent nor the controller's own copy holds a verified/staged non-expired backup.

Order in `RemoveAgent`: resolve → single-destination policy check (as today) → `lastCopies` non-empty
→ 409 plain "PC-1 holds the only verified copy of shop; Sevjala keeps tracking it until another
backup PC holds one too: run 'sevjala backup now' and wait for the copy" (force or not) → only-copies
without force → 409 as today → only-copies with force and `confirm != hostname` →
`ConfirmRequired(…, hostname, len(only), [hostname])` → remove.

- [ ] **Step 1: Write the failing tests** (table): only copy of `shop` on PC-1 → refused even with
  force+confirm, message names `shop`; only copies but `shop` also verified on PC-2 → force without
  confirm 409 word `PC-1`; with `confirm:"PC-1"` → removed; no only copies → removed without force or
  confirm; `lastCopies` pure table incl. staged-only and expired rows.
- [ ] **Step 2: Run** `go test ./coord -run 'Remove|LastCopies'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — Expected: PASS; `go test ./...` PASS (update `cmd/coord.go` call site with
  `""` for now).
- [ ] **Step 5: Commit** `feat: forced removal needs the hostname and never drops a last good copy`.

### Task 6: AD-20 amended on the server

**Files:**
- Modify: `coord/coord.go`, `coord/held.go`, `coord/database.go`, `coord/admin_test.go`,
  `localapi/server.go` (`GET /held/{id}` sets `Content-Disposition`)
- Create: route `GET /db` in `localapi/server.go`, `localapi.DBConfig` in `api.go`,
  `Backend.Database(ctx) (DBConfig, error)`, `coord.Controller.Database`

**Interfaces:**
- Produces: `DBConfig{Host string "host"; Port int "port"; User string "user"; Dumper string "dumper";
  Databases []string "databases"; HasPassword bool "has_password"}`; `Client.Database(ctx)`.

- [ ] **Step 1: Write the failing tests**
  - `TestAdminOnlyActions` rewritten to the spec's "After" column: admin only for `db set --dumper`
    and `restore --file`; every other row `false`, including pair, code, remove, export, host and port
    changes (with password), policy add over a used tag and keep lowered (both with confirm).
  - `TestChangingTheDatabaseHostNeedsThePassword` gains `{Port: ptr(3307)}` → 400 mentioning
    "password"; message "changing the database host, port or user needs its password again: add
    --password (or --no-password)".
  - `TestHeldDownloadName`: `GET /held/{id}` header
    `Content-Disposition: attachment; filename="shop_2026-10-14_00-20-00.sql.gz"` (agent local time).
  - `TestDatabaseConfig`: `GET /db` after a set with password → `HasPassword true`, no password
    field anywhere in the JSON (`!strings.Contains(body, "secret")`).
- [ ] **Step 2: Run** `go test ./coord ./localapi -run 'Admin|Password|Held|DatabaseConfig'` —
  Expected: FAIL.
- [ ] **Step 3: Implement** by deleting the `CallerIsAdmin` checks the spec removes (keep `Dumper`
  and `File`); port joins the password guard.
- [ ] **Step 4: Run** — Expected: PASS; `go test -race ./...` PASS.
- [ ] **Step 5: Commit** `feat: AD-20 amended: admin only for the dump program and restore from a file`.

### Task 7: CLI follows

**Files:**
- Create: `cmd/confirm.go`, `cmd/confirm_test.go`
- Modify: `cmd/policy.go`, `cmd/coord.go` (agents remove), `cmd/restore.go`, `cmd/export.go`,
  `cmd/db.go` (help text), their tests

**Interfaces:**
- Produces: `withConfirm[T any](in io.Reader, out io.Writer, given string, call func(confirm string) (T, error)) (T, error)`
  — calls with `given`; on `AsConfirm` and `given == ""` prints the agent's message, then
  `Type <word> to continue, anything else cancels: `, reads one line (trimmed), and calls again with it
  when it equals `word`; otherwise prints `Cancelled: nothing was changed.` and returns `errSilent1`.

- [ ] **Step 1: Write the failing tests**
  - `TestWithConfirm` (table): no 409 → one call; 409 + typed word → second call with word;
    409 + wrong word → `errSilent1`, one call, "Cancelled" printed; `given` set and still 409 → the
    agent's error returned, no prompt.
  - `TestPolicySetConfirms`, `TestAgentsRemoveForceConfirms` with the existing fake-agent harness:
    `--confirm daily` / `--confirm PC-1` sent through; prompt path via stdin.
  - `TestExportNeedsNoAdmin`: running as non-admin no longer returns `needsAdmin`; Long help no
    longer says "asks for administrator rights".
- [ ] **Step 2: Run** `go test ./cmd -run 'Confirm|Export'` — Expected: FAIL.
- [ ] **Step 3: Implement**; `--confirm WORD` flag on `policy add`, `policy set`, `agents remove`,
  `restore` (skips the replace prompt when it equals the plan's target). `db set --port` help says the
  password is needed.
- [ ] **Step 4: Run** `go test ./cmd` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(cmd): typed confirmation for destructive changes; export without admin`.

### Task 8: UI foundation: api, routes, confirm dialog

**Files:**
- Create: `ui/src/lib/api.js`, `ui/src/lib/api.test.js`, `ui/src/lib/route.js`,
  `ui/src/lib/route.test.js`, `ui/src/Confirm.svelte`
- Modify: `ui/src/App.svelte` (nav and routing), `ui/src/Dashboard.svelte` (imports `api`),
  `ui/src/lib/live.svelte.js` (drop `post`)

**Interfaces:**
- Produces (`api.js`): `export class ConfirmNeeded extends Error { word; deletes; hosts }`;
  `export function errorFrom(status, body)` → `ConfirmNeeded` for `code==="confirm_required"`, an
  `Error` whose message for `admin_required` is `${body.error}: run it from an administrator prompt`,
  else `Error(body.error || "HTTP " + status)`;
  `export async function api(method, path, body)` (adds `X-Sevjala-UI: 1` for non-GET);
  `export const get = (p) => api('GET', p)`.
- Produces (`route.js`): `export const pages = ['', 'backups', 'restore', 'pcs', 'schedules', 'database', 'logs']`;
  `export function parse(hash) → { page, params }` (`#/restore?backup=BK-1` → `{page:'restore', params:{backup:'BK-1'}}`,
  unknown → `{page:'', params:{}}`).
- Produces (`Confirm.svelte`): props `{ need: ConfirmNeeded, onconfirm(word), oncancel() }`; shows
  the message, "Type <word> to continue", a text field; the button is disabled until
  `typed.trim() === need.word`.

- [ ] **Step 1: Write the failing tests** `api.test.js` (three `errorFrom` cases with exact message),
  `route.test.js` (the two `parse` examples, `#/pcs`, `''`).
- [ ] **Step 2: Run** `cd ui && npm test` — Expected: FAIL (missing modules).
- [ ] **Step 3: Implement**; nav labels exactly: Dashboard · Backups · Restore · Backup PCs ·
  Schedules · Database · Logs (controller only; a node keeps the M6 page).
- [ ] **Step 4: Run** `npm test` PASS and `npm run build` succeeds.
- [ ] **Step 5: Commit** `feat(ui): api helper, routes and the typed-confirmation dialog`.

### Task 9: Backups and Restore screens

**Files:**
- Create: `ui/src/lib/matrix.js`, `ui/src/lib/matrix.test.js`, `ui/src/Backups.svelte`,
  `ui/src/Restore.svelte`
- Modify: `ui/src/App.svelte`, `ui/src/lib/format.js` (`exportName`), `ui/src/lib/format.test.js`

**Interfaces:**
- Produces: `matrix(backups, agents, { db = '', tag = '' }) → { columns: [{agent_id, hostname}], rows: [{ b, cells: string[] }], dbs: string[], tags: string[] }`
  — columns are non-controller agents in the order given; a cell is the copy's state or `''`.
- Produces: `exportName(m) → "<db>_<yyyy-mm-dd_hh-mm-ss>"` (browser local time, same as the
  server's `Content-Disposition`).

Backups: filters (database, schedule, include expired → `GET /backups?all=1`), **Back up now**
(database + schedule select; `POST /backup/now {db, tags}`), row **Restore…** (`#/restore?backup=ID`),
**Export** only for ids in `GET /held`: an `<a download>` to `/api/held/{id}` plus a JSON blob of
the manifest named `exportName(m) + '.json'`; Export first refreshes `GET /held` and, when the id is
gone, says so in the page instead of starting a download. Restore: backup select (`GET /backups`),
mode new database (name defaulting to the dry run's `target`) or replace live (dialog with
`ConfirmNeeded` from the server); **Preview** = `POST /restore {…, dry_run:true}`, **Start**, then
`GET /restore` every 2 s while `state === 'running'`; a "Restore from a file" line shows the spec's
administrator note verbatim.

- [ ] **Step 1: Write the failing tests** `matrix.test.js`: two PCs, three backups, filter by db and
  tag, controller excluded, missing copy `''`; `format.test.js` `exportName` for `2026-10-14T00:20:00`
  local → `shop_2026-10-14_00-20-00`.
- [ ] **Step 2: Run** `npm test` — Expected: FAIL.
- [ ] **Step 3: Implement** the lib functions and both screens.
- [ ] **Step 4: Run** `npm test` PASS, `npm run build` OK; Playwright against a dev controller
  (`sevjala ui` from a hand-started agent): list, filter, export download, restore preview and a
  restore into a new database complete.
- [ ] **Step 5: Commit** `feat(ui): Backups matrix and Restore screens`.

### Task 10: Backup PCs actions and Schedules

**Files:**
- Create: `ui/src/lib/cron.js`, `ui/src/lib/cron.test.js`, `ui/src/Schedules.svelte`
- Modify: `ui/src/BackupPCs.svelte`

**Interfaces:**
- Produces: `cronFor({ kind, minute = 0, hour = 2, weekday = 0, day = 1 }) → string` for kinds
  `hourly` (`"<m> * * * *"`), `daily` (`"<m> <h> * * *"`), `weekly` (`"<m> <h> * * <wd>"`),
  `monthly` (`"<m> <h> <d> * *"`); `describe(cron) → string` ("every hour at :05", "every day at
  02:00", "Sundays at 03:00", "on day 1 of each month at 02:00", `""` → "only by hand", anything else
  → the cron text).

Backup PCs adds **Find PCs on this network** (`POST /discover`, list with **Pair**), **Add by
address** (`POST /agents {addr}`), **Pair** form (code + fingerprint prefix, `POST /pair`), per-PC
**Check** (`POST /agents/{id}/ping`) and **Remove** (`DELETE /agents/{id}`; on the plain only-copies
409 offer "Remove anyway" which resends `?force=true` and goes through `Confirm.svelte`).
Schedules: table (name, `describe(schedule)`, keep, destinations or "every backup PC", on/off,
next run), **Add**, **Change**, **Remove**, preset picker + raw cron field; a change first sends
`dry_run:true` and shows "This deletes N backups on …" when `deletes` is non-empty, then the real
request goes through `Confirm.svelte` on 409; **Clean up now** shows `POST /retention/run
{dry_run:true}` then runs it.

- [ ] **Step 1: Write the failing tests** `cron.test.js`: each kind's string; each `describe` case.
- [ ] **Step 2: Run** `npm test` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `npm test`, `npm run build`; Playwright: add a schedule, lower keep (dialog
  appears with the right count, the button enables only on the exact name), pair and remove a PC on
  the dev fleet.
- [ ] **Step 5: Commit** `feat(ui): pair, check and remove backup PCs; schedules`.

### Task 11: Database and Logs screens

**Files:**
- Create: `ui/src/lib/logs.js`, `ui/src/lib/logs.test.js`, `ui/src/Database.svelte`, `ui/src/Logs.svelte`

**Interfaces:**
- Produces: `filterLogs(entries, { level = '', text = '' }) → entries` (level `info` shows all,
  `warn` shows WARN+ERROR, `error` ERROR only; text matches msg, event or `by`, case-insensitive).

Database: `GET /db` fills host, port, user and the checked databases; **Detect** (`POST /db/detect`)
lists tools and databases with checkboxes; **Save** sends only changed fields (`PUT /db`); the
password field is write-only and becomes required when host, port or user changed; the dump tool is
read-only with the spec's administrator note verbatim. Logs: `GET /logs?limit=500`, newest first,
columns time, level, event, message, by; level select, text box, **Refresh**.

- [ ] **Step 1: Write the failing tests** `logs.test.js`: the three levels and a text match on `by`.
- [ ] **Step 2: Run** `npm test` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `npm test`, `npm run build`; Playwright: change the database list and see the
  `db_configured` line with `by` in Logs; changing the port without a password is blocked in the form.
- [ ] **Step 5: Commit** `feat(ui): Database and Logs screens`.

### Task 12: Records, full verify, Windows check

**Files:**
- Modify: `docs/BLUEPRINT.md` (AD-20 row replaced by the spec's text, M7 row status),
  `docs/protocol.md` (admin table, `confirm_required`, `dry_run`, `GET /logs`, `GET /db`,
  `Content-Disposition`, `by`, port needs password, `RemoveAgent` `confirm`), `docs/decisions.md`
  (D-089 AD-20 amended; D-090 last good copy; D-091 confirm protocol incl. `hosts` and offline-PC
  preview; D-092 `GET /db`; D-093 browser export = server stream + client-side manifest JSON),
  `docs/smoke-tests.md` (M7: normal-user Windows session does each screen action; UAC only for
  dumper and restore-from-file; Logs shows `by`), `docs/PROGRESS.md`, `README.md`.

- [ ] **Step 1:** Full verify order: gofmt list empty; `go vet` host/windows/windows-386/linux/
  `-tags tray`; `golangci-lint run` same set; `go test -race ./...`; `cd ui && npm test`;
  `make build-all` — Expected: all clean.
- [ ] **Step 2:** Deploy to the Windows test PC with `sevjala update --from` and, as a normal user
  over ssh, run `sevjala policy set daily --keep 1` (prompt appears), `sevjala agents remove` on a
  test PC (confirmation), `sevjala backups export latest --to .` (no UAC) — Expected: each as the
  spec says; the desktop browser screens are left as owner smokes.
- [ ] **Step 3: Commit** `docs: M7 records, protocol and smoke tests`.
