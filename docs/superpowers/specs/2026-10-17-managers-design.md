# Managers: control the controller from a backup PC

Owner request 2026-10-17, design approved in chat the same day ("ok good, lets implement").
New architecture decision AD-21 (below), approved with the design.

## Intent

The database PC (controller) often sits in a back room. The operator works at another PC that is
already a paired backup PC (node). A **manager** is such a node that the controller trusts to run
controller commands: from it, `sevjala --controller <command>` and `sevjala ui --controller` do
on the controller what an ordinary user at the controller's keyboard can do since M7.

Goals:

- Every normal-user controller operation (the M7 set) works from a manager, CLI and dashboard.
- The M7 screens are reused unchanged; only the transport under `/api/*` changes.
- Setting up a manager is one admin command on the controller; turning it off is immediate.

Non-goals: managers that are not paired nodes; several controllers per node; administrator
actions from a manager; managing nodes from a manager (nodes have no remote commands); a
dashboard reachable from the LAN by browser.

Anti-goals (a failure even if it works): a backup PC that opens a LAN connection; a new listening
port or route on the database PC; a manager able to do what an admin at the controller must do;
a change made from a manager that the controller's log does not attribute to it.

## AD-21 (new): manager links

| ID | Decision | Rationale |
|---|---|---|
| AD-21 | Accepted 2026-10-17, amends AD-14 and AD-20: an administrator on the controller marks a paired node as a **manager**. The controller keeps a few spare connections to each online manager, opened like every other LAN call (controller dials, mutual TLS, pinned certificates), then switched with HTTP `Upgrade: sevjala-manage`; on a switched connection the node sends HTTP requests and the controller answers them with its local API, as a non-admin caller labelled with the manager. The node's local API relays `/controller/…` through those links; `sevjala --controller …` and `sevjala ui --controller` use that relay. Admin-only actions are refused from a manager. | Control from another desk without breaking AD-02 (nodes never initiate) or opening anything new on the database PC; the pairing certificates already authenticate both ends. |

## How it works

### Marking a manager (controller)

- Store: `agents.manager` (0/1), new migration. `localapi.Agent` gains `manager`.
- `sevjala agents manager <agent> on|off` → local API `PUT /agents/{agent}/manager`
  `{"manager":true|false}`. Admin only (AD-20 list grows by this one item). Only a paired node can
  be a manager; the controller itself cannot. Turning off closes that PC's links at once.
- `sevjala agents` gains a MANAGER column (`yes` / `-`). The Backup PCs screen shows "Manager:
  yes/no" in the PC's details with the command to change it (the dashboard runs as a normal user,
  so it shows the command rather than a button).
- Removing a manager PC (`agents remove`) closes its links like turning it off.

### Telling the node

Every 15-second `GET /status` poll carries two request headers: `X-Sevjala-Manager: 1|0` and
`X-Sevjala-Controller-Host: <controller hostname>`. The node keeps the latest values in memory
(they are re-learned within 15 s after a restart). The node's own `/status` (local API) gains:

```json
"controller": {"hostname":"SERVER-01","manager":true,"links":2,"last_contact":"…"}
```

(`controller` is absent before the first poll after start; `links` = idle links waiting.)

### The link (LAN API)

New node route `POST /manage`, paired controller certificate only (like `/status`), headers
`Connection: Upgrade`, `Upgrade: sevjala-manage`. The node refuses (403) when the latest poll said
it is not a manager. Otherwise it answers `101 Switching Protocols`, takes over the TLS connection
and puts it in its link pool. From then on the node is the HTTP/1.1 client and the controller the
server on that connection; TLS is unchanged.

Controller side, per online manager: keep **4** links. When one closes (idle timeout, error,
node restart), dial a new one; dial failures back off 1 s → 2 s → … → 15 s. A link idle for
**5 minutes** is closed by the controller and replaced. Keepers start and stop after each poll
round: started for manager PCs that answered, stopped for PCs no longer managers, removed, or
offline/error. Every request on a link re-checks that the PC is still a manager (403 and the link
closes if not).

The controller serves `localapi.NewHandler` on each link with a request context that says: not
admin, `by` = `<account> via <manager hostname>`, where `<account>` is the `X-Sevjala-By` header
the node sends (the node's own caller account; a label only, trusted because the manager is
authenticated) and `<manager hostname>` is the controller's own record of that PC.

Admin-only actions requested through a link answer 403 with code `controller_admin_required` and
the message `<action> needs administrator rights on <controller hostname> itself`. The CLI never
offers to elevate for it, and the dashboard shows the message as is.

### The relay (node local API)

Node local API route `/controller/<route>` (any method) forwards `<route>`, query and body to the
controller through a link, adds `X-Sevjala-By: <caller account>`, and streams the answer back
(including `GET /held/{id}` downloads). Waiting for a free link takes at most 20 s. Errors, all
JSON `{"error"}`:

| Situation | Answer |
|---|---|
| the agent is a controller | 409 `this PC is the controller; run the command without --controller` |
| not paired | 409 `this PC is not paired with a controller` |
| not polled since start | after 20 s: 503 `the controller has not contacted this PC since Sevjala started here (is it on and on the network?)` |
| latest poll says not a manager | 403 `this PC is not a manager of <host> (on <host>, an administrator runs: sevjala agents manager <this hostname> on)` |
| manager, no link within 20 s | 503 `<host> has not connected to this PC for <duration> (is it on and on the network?)` |
| the link breaks mid-request | 502 `the link to <host> broke mid-way; try again` |

### CLI (`--controller`)

- Global flag `--controller` (bool). It is allowed with the commands that run on the controller's
  local API: `status`, `discover`, `pair`, `agents` (all sub-commands but `manager`), `ping`,
  `db`, `backup`, `backups`, `restore`, `policy`, `retention`. With any other command it is a usage
  error naming that list.
- One chokepoint: the client every command uses comes from the globals, which add the
  `/controller` prefix when the flag is set (`callAgent` and the places that open a client
  directly today, such as export).
- `sevjala --controller status` prints the controller's status headed
  `<host> (controller, via manager link)`.
- `--controller` on the controller itself fails with the relay's 409 message.
- `controller_admin_required` is shown as the error; no UAC prompt.
- Files a command writes (export) land on the manager PC; `restore --file` is admin-only and so is
  refused, as is `db set --dumper`.

### Dashboard (`sevjala ui --controller`)

- `sevjala ui --controller` starts the same dashboard process; its `/api/*` and `/events` go to
  the relay. The page shows a banner "SERVER-01 · controller — opened from BACKUP-PC-1"
  (`/events` state carries `via` = the node's hostname in this mode).
- Plain `sevjala ui` on a manager shows the node's dashboard with a line: "This PC can manage
  SERVER-01: open its dashboard with `sevjala ui --controller` or the tray menu."
- Tray on a manager node: menu item "Open controller dashboard" (runs `sevjala ui --controller`).

### Logs and events

New events: `manager_set` (on/off, by), `manager_link_failed` (once per failure streak, with the
reason), and the controller's existing change events carry `by` = `<account> via <manager>`. The
node logs nothing per request.

## Constraints

- Go stdlib + the blueprint's libraries only; `CGO_ENABLED=0`; builds with go-win7.
- No change to ports, firewall rules, discovery, or pairing.
- Nodes still never dial; the database PC opens no new port or route.
- Secrets never cross a link except as the existing API carries them (the DB password is
  write-only through `PUT /db`, as locally).
- `docs/protocol.md` documents `/manage`, the poll headers, the relay, the new status block, the
  new local API route and error code.

## Left to the builder

Package layout (a small `manage/` package for the link pool, keeper and relay is expected);
exact link bookkeeping; how `/events` learns `via`; test harness shape (loopback controller +
node agents, like `coord`'s integration tests).

## Calls I made (owner can overrule)

- Marking a manager needs administrator rights on the controller.
- Every interactive account on the manager PC may use the relay (same rule as its own local API,
  AD-20). The manager PC's users are trusted like the controller's own normal users.
- 4 links per manager, 5-minute idle close, 20-second wait, 1–15 s redial backoff.
- No "make manager" button in the dashboard; it shows the command.

Settled after the builder check:

- **Timeouts:** with `--controller`, every CLI call and the dashboard proxy's upstream calls
  allow at least 30 s, so the relay's 20-second wait and its 503 message reach the user.
- **One admin chokepoint:** `localapi.AdminRequired` takes the request context; on a request
  that came over a manager link it returns `controller_admin_required` with the
  "on <controller> itself" message. Every admin check already goes through it.
- **Re-pairing:** a removed PC that is paired again is not a manager until marked again (the
  flag goes with the agent row; a new pairing starts at 0).
- **Mixed versions:** a node that answers `POST /manage` with 404 is an old build:
  `manager_link_failed` says "update Sevjala on <PC>". A poll without `X-Sevjala-Manager` means
  "not a manager".
- **Wording keyed on `via`:** with `via` set, the dashboard's "from an administrator prompt" hints
  read "on <controller> itself", and the footer's "This PC" reads the controller's name. These
  and the banner are the only screen changes.
- **Handover hygiene:** the node clears the deadlines the HTTP server set on a connection before
  pooling it as a link, and closes the pool (and every link) on shutdown.
