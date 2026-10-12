# M1 Coordination — design

Date: 2026-10-12 · Milestone: M1 ([Roadmap](../../BLUEPRINT.md#roadmap)) · Branch: `p0-foundation`
(the owner keeps P0 and M1 on one branch while the P0 manual smokes are pending)

## Goal and exit criterion

Agents know who they are, a Controller finds Nodes on the LAN, pairs with them by a 6-digit
code, and talks to them over mutual TLS.

**Exit:** discover → pair → ping across two real PCs; an unpaired peer is rejected at TLS.

Blueprint sections in play: Coordination protocol, Security model, Data model (identity,
config, agents table).

## Scope

In:
- Packages `identity`, `pairing`, `discovery`, `lanapi`, `store`, `coord` (new), plus changes to
  `localapi`, `svc`, `cmd`, `logx`.
- CLI `sevjala discover`, `code`, `pair <agent> --code N`, `agents`, `agents add <host:port>`,
  `ping <agent>`.
- LAN API `GET /identity`, `POST /pair`, `GET /status` (M1 subset).
- Install: data dir lockdown (moved here from the M2 gate), Windows firewall rules (D-007).
- `doctor`: identity, LAN listener, data dir lockdown, firewall (Windows).

Out: `/status` polling and offline detection (M3), the `store` / `backups` fields of `/status`
(M2), keyring (M2, for the DB password), unpair command, a second controller per node.

## Decisions taken in brainstorming (owner)

- Data dir lockdown lands in M1, because M1 writes the first private key.
- Controller store is SQLite (`modernc.org/sqlite`) from M1.
- The pairing code lives only in the node agent's memory; an agent restart clears it.
- While a code is active a node accepts any client certificate for `/identity` and `/pair`
  only; a successful pair replaces the trusted controller.

## Identity (`identity`)

Files in `<data-dir>/identity/` (dir 0700 on Unix; Windows inherits the locked data dir ACL):

| File | Content |
|---|---|
| `agent_id` | UUIDv4 text, generated once |
| `cert.pem` | self-signed X.509, ECDSA P-256, CN = agent_id, 10 years, ServerAuth + ClientAuth |
| `key.pem` | PKCS#8 EC key, 0600 |
| `peers.json` | node only, once paired: `{"v":1,"controller":{"agent_id":"…","fp":"sha256:…","paired_at":"RFC3339"}}` |

`LoadOrCreate`: no `agent_id` file → generate everything, write key and cert first and
`agent_id` last (each via temp file + rename), log `identity_created`. `agent_id` present but
the cert or key missing, unparsable, mismatched, or CN ≠ agent_id → error; the identity is
never silently replaced. Fingerprint = `sha256:` + lowercase hex of SHA-256(cert DER).

## Pairing code (`pairing`)

`crypto/rand` 6 digits, valid 10 minutes, one active code per agent (issuing replaces it).
`Proof(code, controllerFP, nodeFP) = hex(HMAC-SHA256(key=code, msg=controllerFP + ":" + nodeFP))`.
`Check` compares in constant time; a wrong proof counts one attempt and the 5th wrong one burns
the code; success clears it. The code is never logged or written to disk.

## Discovery (`discovery`)

- Request datagram: `SEVJALA/1 DISCOVER <nonce>`, nonce = 16 lowercase hex chars.
- Every agent runs a responder on UDP `0.0.0.0:<discovery_port>`; it ignores anything else
  (and anything over 256 bytes) and replies by unicast to the sender with
  `{"v":1,"nonce":"…","agent_id":"…","hostname":"…","port":<LAN port>,"role":"…","version":"…","os":"<GOOS>","fp":"sha256:…"}`.
- The controller probes from an ephemeral UDP port to the subnet broadcast address of every up,
  broadcast-capable, non-loopback IPv4 interface, collects for 2 s, drops wrong nonces and
  bad JSON, dedupes by agent_id, and drops its own agent_id. Tests probe `127.0.0.1:<port>`.

## LAN API (`lanapi`)

One TCP listener on `listen`, TLS 1.2+, our identity cert, `ClientAuth: RequestClientCert`.
Client certificate rule, checked during the handshake (per connection, so the log has the
remote address):

| Client presents | Handshake |
|---|---|
| no certificate | accepted; only `GET /identity` is served |
| the pinned controller's cert | accepted; everything is served |
| any other cert, node has an active pairing code | accepted; `/identity` and `/pair` only |
| any other cert, otherwise | **rejected** (`peer_rejected` logged) |

Routes:

| Route | Who | Response |
|---|---|---|
| `GET /identity` | anyone | `{"agent_id","hostname","role","version","fp"}` |
| `POST /pair` | node; client cert required | body `{"controller_id","controller_fp","proof"}`; 409 on a controller; 403 if the client cert's fp ≠ `controller_fp`, no active code, wrong proof, or burnt; 200 `{"agent_id","fp"}` and `peers.json` written on success |
| `GET /status` | pinned controller | `{"agent_id","role","version","uptime_s","health":"healthy","backups":[],"problems":[]}`; 403 otherwise |
| anything else | | 404 `{"error":"not found"}` |

Request bodies are capped at 4 KiB. The client (controller side) never verifies a CA: it sets
`InsecureSkipVerify` and pins the expected node fingerprint in `VerifyPeerCertificate`; it
presents its own cert for `/pair` and `/status` and none for `/identity` (so `agents add`
works against a node already paired elsewhere). A TLS rejection by the node is reported as
`<addr> rejected this controller's certificate (not paired with it)`.

## Controller store (`store`)

`<data-dir>/controller.db`, opened only by a controller agent, `busy_timeout` 5 s. Schema
version in `PRAGMA user_version`; migration 1 creates the blueprint's `agents` table
(`agent_id` PK, hostname, last_ip, port, fp, role, version, paired_at, last_seen, state,
free_bytes). Times are UTC RFC 3339 text, empty when unset.

## Role logic (`coord`) and the local API

`coord.Service` implements `localapi.Backend`; it holds a `Node` part or a `Controller` part
by role, and a call for the other role returns 409 `this agent is a node` / `… a controller`.

| Local route | Role | Does |
|---|---|---|
| `GET /status` | both | P0 fields plus `agent_id`, `fp` |
| `POST /code` | node | issue a code → `{"code","expires_at"}`; logs `pair_code_issued` (no code in the log) |
| `POST /discover` | controller | probe; cache results in memory; refresh hostname/address of a paired agent whose fp matches → `{"agents":[…]}` with `paired` per entry |
| `POST /agents` | controller | body `{"addr"}`: `GET /identity` at addr, check the body fp equals the TLS cert fp, cache it → one entry; logs `agent_added` |
| `GET /agents` | controller | paired agents from the store |
| `POST /pair` | controller | body `{"agent","code"}`: resolve among cached entries, pin the cached fp, send the proof, store the agent → the agent; logs `pair_succeeded` |
| `POST /agents/{agent}/ping` | controller | resolve among paired agents, `GET /status` over mTLS, update `last_seen`/`version`/`state=online` → `{"agent","rtt_ms","status"}` |

`<agent>` is resolved as: exact agent_id, else exact hostname (case-insensitive), else a unique
agent_id prefix of at least 4 characters; ambiguity lists the candidates. Errors travel as
`{"error":"…"}` with the right status; the client returns them as `*localapi.Error`.
The local client timeout grows from 3 s to 15 s (discover waits 2 s, pair does a TLS round
trip); `status` keeps its own 3 s context.

## CLI

| Command | Output |
|---|---|
| `sevjala discover` | table `AGENT HOSTNAME ADDRESS ROLE VERSION PAIRED`, agent_id shortened to 8 chars; none → hint about firewall / `agents add` |
| `sevjala code` | `pairing code 123456, valid until 16:42` and the command to run on the controller |
| `sevjala pair <agent> --code N` | `paired with BACKUP-PC-01 (3f2a9c1e) sha256:…`; `--code` must be 6 digits (exit 2) |
| `sevjala agents` | table `AGENT HOSTNAME ADDRESS STATE LAST SEEN` |
| `sevjala agents add <host:port>` | the entry, then the `pair` hint; port defaults to 17891 |
| `sevjala ping <agent>` | `BACKUP-PC-01 (192.168.1.21:17891) answered in 4 ms: node 0.2.0, healthy, up 2h3m`; failure exit 1 |

All talk to the local agent only; an agent that does not answer is exit 1 with the endpoint.

## Agent runtime (`svc.Agent`)

Start: open log → load or create identity → controller: open store → local API → LAN API on
`listen` → discovery responder on `discovery_port` (reporting the LAN API's actual port).
Any failure stops what already started and returns the error. Stop: reverse order.

## Install, uninstall, doctor

- **Lockdown** (`svc.LockDataDir`, before anything is written): create the dir if missing.
  Unix: owner must be uid 0 (else refuse), then `chown 0:0`, `chmod 0700`. Windows: owner must be
  Administrators or SYSTEM (else refuse), then set owner Administrators and a protected DACL
  `D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)` (propagates to existing children). Refusal message names
  the owner and says to remove or re-own the dir.
- **Firewall** (Windows): rules `Sevjala-LAN-API` (TCP, listen port) and `Sevjala-Discovery`
  (UDP, discovery port), inbound allow, all profiles, via `netsh advfirewall firewall`;
  delete-then-add on install, delete on uninstall. No-op elsewhere.
- **Doctor** adds: `identity` (agent_id + fp; FAIL if unloadable), `lan api` (`GET /identity`
  on `127.0.0.1:<listen port>` returns our fp), `lockdown` (owner/mode or owner/DACL as above),
  `firewall` (Windows only: both rules exist).

## Events (provisional until the PRD event list is in the repo)

`identity_created`, `discovery_run` (count), `agent_added`, `pair_code_issued`,
`pair_succeeded`, `pair_rejected` (reason, remote), `peer_rejected` (fp, remote). No event
carries a code, a proof or a key.

## Testing

- `identity`: create/load round trip, fp format, key mode 0600, CN = agent_id, missing
  `agent_id` regenerates, missing cert with `agent_id` errors, peers round trip and absence.
- `pairing`: 6 digits, success clears, wrong proof, 5th wrong burns, expiry (fake clock),
  re-issue replaces.
- `discovery`: loopback probe, wrong nonce dropped, junk ignored, dedupe, self dropped,
  broadcast address math.
- `lanapi`: real TLS on loopback: identity without cert, pair, status after pair, unpaired
  cert rejected at the handshake (no HTTP status), wrong code 403, fp mismatch 403, status
  without cert 403, re-pair replaces, controller answers `/pair` with 409.
- `store`: migrate twice, upsert/list/get, seen update, not found.
- `coord`: resolution rules; **loopback integration**: controller + node in process, discover →
  pair → ping, then a second controller is rejected at TLS.
- `localapi`: every route against a fake backend, error mapping both ways.
- `svc`: firewall command lines; lockdown on Unix (root-only tests skip otherwise, run in
  Docker as root); unlocked-dir detection.
- `cmd`: renderers from fixed input; `--code` validation.
- Manual (`docs/smoke-tests.md`): two real PCs, and the Windows ACL/firewall on the Win7 VM.

## Docs touched

`docs/protocol.md` (discovery datagrams, LAN routes, identity files, `peers.json`, agents
table, new local routes), `docs/decisions.md` (D-010…), `docs/smoke-tests.md` (M1),
`docs/PROGRESS.md`, `README.md`.
