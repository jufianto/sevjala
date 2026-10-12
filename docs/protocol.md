# Protocol and on-disk formats

The living contract for every wire message and file format. Update it in the same PR as
any change to one (definition of done).

Formats not implemented yet are still defined by the blueprint:

| Format | Blueprint section | Status |
|---|---|---|
| Discovery datagrams | [Coordination protocol → Discovery](./BLUEPRINT.md#discovery-controller--all) | implemented below (M1) |
| Pairing (`POST /pair`, HMAC proof) | [Coordination protocol → Pairing](./BLUEPRINT.md#pairing-code-based-trust-bootstrap) | implemented below (M1) |
| LAN API routes and headers | [Coordination protocol → LAN API](./BLUEPRINT.md#lan-api-served-by-every-agent-used-only-by-the-controller) | `/identity`, `/pair`, `/status` below; `/backups/*` from M2 |
| Local API routes | [Coordination protocol → Local API](./BLUEPRINT.md#local-api-pipe--socket-http-no-tls) | implemented routes below |
| `config.toml` | [Data model → config.toml](./BLUEPRINT.md#configtoml-minimal-example) | top-level keys implemented |
| Manifest `<backup_id>.json` | [Data model → Manifest](./BLUEPRINT.md#manifest-backup_idjson-identical-on-controller-and-nodes) | M2 |
| `GET /status` response | [Data model → GET /status](./BLUEPRINT.md#get-status-response) | M1 subset below |
| Node store directory | [Data model → Node store directory](./BLUEPRINT.md#node-store-directory) | M2 |

Fingerprints everywhere are `sha256:` + 64 lowercase hex of SHA-256 over the DER certificate.

## Identity files (since M1)

`<data-dir>/identity/` (0700 on Unix; Windows inherits the locked data dir ACL):

| File | Content |
|---|---|
| `agent_id` | UUIDv4 + newline; written last at creation |
| `cert.pem` | self-signed X.509, ECDSA P-256, CN = agent_id, 10 years, ServerAuth + ClientAuth |
| `key.pem` | PKCS#8 `PRIVATE KEY`, mode 0600 |
| `peers.json` | node only, after pairing (mode 0600) |

`peers.json` v1:

```json
{"v": 1, "controller": {"agent_id": "c960acf5-…", "fp": "sha256:…", "paired_at": "2026-10-12T09:00:00Z"}}
```

An `agent_id` without a loadable, matching cert and key is an error, never a regeneration.

## Discovery (since M1)

UDP, `discovery_port` (default 17890). Request, sent by the controller to each subnet
broadcast address:

```
SEVJALA/1 DISCOVER <nonce>
```

`<nonce>` is 16 lowercase hex chars; anything else, or a datagram over 256 bytes, is ignored.
Reply, unicast to the sender:

```json
{"v":1,"nonce":"…","agent_id":"…","hostname":"BACKUP-PC-01","port":17891,"role":"node","version":"0.2.0","os":"windows","fp":"sha256:…"}
```

| Field | Meaning |
|---|---|
| `v` | 1 |
| `nonce` | echoed; replies with another nonce are dropped |
| `port` | the agent's LAN API TCP port |
| `role` | `controller` or `node` |
| `os` | Go `GOOS` |
| `fp` | the agent's certificate fingerprint; pairing pins it |

The controller collects for 2 s, dedupes by `agent_id`, and drops its own reply.

## LAN API (since M1)

HTTPS on `listen` (default `0.0.0.0:17891`), TLS 1.2+, the agent's own certificate, client
certificate requested but not required. Handshake rule on the node:

| Client presents | Result |
|---|---|
| no certificate | accepted; only `GET /identity` succeeds |
| the paired controller's certificate | accepted |
| another certificate while a pairing code is active | accepted; only `/identity` and `/pair` succeed |
| another certificate otherwise | handshake fails (`bad certificate` alert), event `peer_rejected` |

Session tickets are disabled (a resumed session would skip the certificate check) and idle
connections close after 1 minute; every route but `/identity` and `/pair` re-checks the client
certificate against the paired controller. Errors are `{"error": "<message>"}`. Request bodies are capped at 4 KiB.

| Route | Auth | Success | Errors |
|---|---|---|---|
| `GET /identity` | none | `{"agent_id","hostname","role","version","fp"}` | — |
| `POST /pair` | client cert | `{"agent_id","fp"}` (the node) | 403 cert fp ≠ `controller_fp`, wrong proof, the proof that burns the code, no cert; 400 bad JSON; 409 on a controller (reachable only without a cert). With no active code a new controller never gets here: its handshake fails. |
| `GET /status` | paired controller cert | see below | 403 otherwise |
| anything else | | | 404 `not found` |

`POST /pair` body: `{"controller_id":"…","controller_fp":"sha256:…","proof":"<hex>"}` with
`proof = hex(HMAC-SHA256(key = code, msg = controller_fp + ":" + node_fp))`. Success writes
`peers.json`, replaces any previous controller, and burns the code. Five wrong proofs burn
the code; the code lives 10 minutes in the node agent's memory only.

`GET /status` (M1 subset; M2 adds `store`):

```json
{"agent_id":"…","role":"node","version":"0.2.0","uptime_s":86400,"health":"healthy","backups":[],"problems":[]}
```

## Local API

Transport: HTTP/1.1 over `\\.\pipe\sevjala` (Windows, SDDL `D:P(A;;GA;;;BA)(A;;GA;;;SY)`) or
`/var/run/sevjala.sock` (macOS/Linux, `0660 root:sevjala`). A non-default data dir uses
`<data-dir>/sevjala.sock` or `\\.\pipe\sevjala-<first 8 hex of sha256(data dir)>` (default DACL).

Errors are `{"error": "<message shown to the operator>"}`; a route for the other role is 409
(`this agent is a node; run this on the controller` / `this agent is a controller; run this
on a node`); a LAN failure behind a route is 502. Bodies are capped at 4 KiB.

| Route | Role | Request | Response |
|---|---|---|---|
| `GET /status` (P0) | both | — | below |
| `POST /code` (M1) | node | — | `{"code":"123456","expires_at":"RFC3339","agent_id":"…"}` |
| `POST /discover` (M1) | controller | — | `{"agents":[Discovered…]}` |
| `POST /agents` (M1) | controller | `{"addr":"host[:port]"}` (port defaults to 17891) | `Discovered` |
| `GET /agents` (M1) | controller | — | `{"agents":[Agent…]}` |
| `POST /pair` (M1) | controller | `{"agent":"<agent>","code":"123456"}` | `Agent` |
| `POST /agents/{agent}/ping` (M1) | controller | — | `{"agent":Agent,"rtt_ms":4,"uptime_s":7380,"health":"healthy"}` |
| anything else | | | 404 `not found` |

`Discovered`: `{"agent_id","hostname","addr":"ip:port","role","version","os","fp","paired":bool}`.
`Agent`: `{"agent_id","hostname","addr","role","version","fp","paired_at","last_seen","state"}`
(`last_seen` omitted when never seen). `<agent>` is an exact agent_id, else a hostname (any
case), else a unique agent_id prefix of at least 4 characters; ambiguity is 409 listing the
candidates, no match is 404.

### `GET /status`

`200 application/json`

```json
{"version": "0.2.0", "role": "node", "pid": 4120, "uptime_s": 133, "data_dir": "C:\\ProgramData\\Sevjala", "agent_id": "…", "fp": "sha256:…"}
```

| Field | Type | Meaning |
|---|---|---|
| `version` | string | agent build version (`dev` for local builds) |
| `role` | string | `controller` or `node` |
| `pid` | int | agent process id |
| `uptime_s` | int | seconds since the agent started |
| `data_dir` | string | absolute data dir |
| `agent_id` | string | since M1 |
| `fp` | string | since M1 |

## Controller store (since M1)

`<data-dir>/controller.db`, SQLite, schema version in `PRAGMA user_version` (a newer version
than the binary knows is refused). Version 1:

```sql
CREATE TABLE agents (
  agent_id   TEXT PRIMARY KEY,
  hostname   TEXT NOT NULL,
  last_ip    TEXT NOT NULL,
  port       INTEGER NOT NULL,
  fp         TEXT NOT NULL,
  role       TEXT NOT NULL,
  version    TEXT NOT NULL,
  paired_at  TEXT NOT NULL,          -- UTC RFC 3339
  last_seen  TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL DEFAULT 'online',
  free_bytes INTEGER NOT NULL DEFAULT 0
);
```
