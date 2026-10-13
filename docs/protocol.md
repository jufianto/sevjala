# Protocol and on-disk formats

The living contract for every wire message and file format. Update it in the same PR as
any change to one (definition of done).

Formats not implemented yet are still defined by the blueprint:

| Format | Blueprint section | Status |
|---|---|---|
| Discovery datagrams | [Coordination protocol → Discovery](./BLUEPRINT.md#discovery-controller--all) | implemented below (M1) |
| Pairing (`POST /pair`, HMAC proof) | [Coordination protocol → Pairing](./BLUEPRINT.md#pairing-code-based-trust-bootstrap) | implemented below (M1) |
| LAN API routes and headers | [Coordination protocol → LAN API](./BLUEPRINT.md#lan-api-served-by-every-agent-used-only-by-the-controller) | `/identity`, `/pair`, `/status`, `PUT /backups/{id}` below; `GET`/`DELETE /backups/{id}` from M3/M4 |
| Local API routes | [Coordination protocol → Local API](./BLUEPRINT.md#local-api-pipe--socket-http-no-tls) | implemented routes below |
| `config.toml` | [Data model → config.toml](./BLUEPRINT.md#configtoml-minimal-example) | implemented; written whole by the agent (M2) |
| Manifest `<backup_id>.json` | [Data model → Manifest](./BLUEPRINT.md#manifest-backup_idjson-identical-on-controller-and-nodes) | implemented below (M2) |
| `GET /status` response | [Data model → GET /status](./BLUEPRINT.md#get-status-response) | implemented below (M2) |
| Node store directory | [Data model → Node store directory](./BLUEPRINT.md#node-store-directory) | implemented below (M2) |

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
certificate against the paired controller. Errors are `{"error": "<message>"}`. Request bodies are capped at 4 KiB,
except `PUT /backups/{id}`.

| Route | Auth | Success | Errors |
|---|---|---|---|
| `GET /identity` | none | `{"agent_id","hostname","role","version","fp"}` | — |
| `POST /pair` | client cert | `{"agent_id","fp"}` (the node) | 403 cert fp ≠ `controller_fp`, wrong proof, the proof that burns the code, no cert; 400 bad JSON; 409 on a controller (reachable only without a cert). With no active code a new controller never gets here: its handshake fails. |
| `GET /status` | paired controller cert | see below | 403 otherwise |
| `PUT /backups/{id}` | paired controller cert | see below | see below |
| `DELETE /backups/{id}` (M3) | paired controller cert | see below | see below |
| anything else | | | 404 `not found` |

`POST /pair` body: `{"controller_id":"…","controller_fp":"sha256:…","proof":"<hex>"}` with
`proof = hex(HMAC-SHA256(key = code, msg = controller_fp + ":" + node_fp))`. Success writes
`peers.json`, replaces any previous controller, and burns the code. Five wrong proofs burn
the code; the code lives 10 minutes in the node agent's memory only.

`GET /status` (since M2; `store` only on a node; `min_free_bytes` since M3):

```json
{"agent_id":"…","role":"node","version":"0.3.0","uptime_s":86400,
 "store":{"path":"C:\\ProgramData\\Sevjala\\store","free_bytes":512000000000,"min_free_bytes":2147483648},
 "health":"healthy",
 "backups":[{"backup_id":"BK-20261013-101500-A8F2","size":214748364,"sha256":"…","state":"verified"}],
 "problems":[]}
```

`state` is `verified` (manifest present, file size matches) or `corrupt`; hashes are not
recomputed here. `health` is `warning` when `problems` is not empty: a corrupt backup, an
unreadable store, a partial file removed since the last scan (reported once), or (since M3)
`free space <n> is below the floor <m>`. When the store cannot be read, `backups` is `null`:
the inventory is unknown, and the controller makes no retention decision for that node.

The controller polls this route on every paired node every 15 s (since M3). An answer makes
the node `online`, or `warning` when `health` is not `healthy`; three missed polls in a row
make it `offline`; a TLS rejection or a changed certificate makes it `error` at once.

### `PUT /backups/{id}` (since M2)

Paired controller only. The body is the `.sql.gz` file. Headers:

| Header | Value |
|---|---|
| `X-Sevjala-Sha256` | 64 lowercase hex of the body |
| `X-Sevjala-Size` | body length in bytes (also `Content-Length`) |
| `X-Sevjala-Manifest` | the manifest JSON, standard base64, at most 8 KiB |
| `Expect` | `100-continue`: refusals and "already stored" cost no body bytes |

The manifest's `backup_id`, `sha256` and `size` must equal the path and headers. The node
writes `<id>.part` while hashing, then renames it and writes the manifest (see Node store).
Each body read must arrive within 60 s; the controller cancels after 60 s without progress.

| Status | Meaning |
|---|---|
| 200 `{"backup_id","sha256","size"}` | stored and verified, or an identical copy was already stored (body not read) |
| 400 | bad id, headers or manifest |
| 403 | not the paired controller |
| 409 | the id is stored with another sha256, or the agent is a controller |
| 503 | the id is being received right now (a vanished controller's transfer holds it up to 60 s); retried |
| 422 | the received bytes do not match `size`/`sha256`; nothing is kept |
| 507 | storing it would leave less than `min_free_bytes` free |
| 500 | anything else (disk error) |

## Manifest (since M2)

`<backup_id>.json` next to `<backup_id>.sql.gz`, identical on the controller and every node
(written 0600, indented):

```json
{
  "v": 1,
  "backup_id": "BK-20261013-101500-A8F2",
  "database": "shop",
  "created_at": "2026-10-13T10:15:00+07:00",
  "source_agent": "c960acf5-…",
  "server_version": "10.4.32-MariaDB",
  "dump_flags": ["--routines", "--triggers", "--events", "--hex-blob", "--no-create-db",
                 "--default-character-set=utf8mb4", "--single-transaction"],
  "size": 214748364,
  "sha256": "…",
  "tags": ["manual"]
}
```

| Field | Rule |
|---|---|
| `v` | 1 |
| `backup_id` | `^BK-\d{8}-\d{6}-[0-9A-F]{4}$`: local time of the dump, then 4 random uppercase hex; it is also the file name |
| `created_at` | RFC 3339, whole seconds |
| `dump_flags` | content flags, then `--single-transaction` (all tables InnoDB) or `--lock-tables` |
| `size`, `sha256` | of the `.sql.gz` file (gzip of the mysqldump output) |
| `tags` | `manual` unless `backup now --tag` |

### `DELETE /backups/{id}` (since M3)

Paired controller only; the only way a stored backup is removed (blueprint AD-08). Body
`{"reason":"…"}` (at most 4 KiB, the reason must not be empty). The node removes
`<id>.sql.gz`, then `<id>.json`, syncs the directory and logs `backup_deleted` with the reason.

| Status | Meaning |
|---|---|
| 200 `{"backup_id","deleted":bool}` | removed; `deleted` is false when nothing was there (not an error) |
| 400 | bad id, or no reason |
| 403 | not the paired controller |
| 503 | the id is being received right now |
| 409 | the agent is a controller |

## Node store (since M2)

Default `<data-dir>/store` (inherits the locked data dir), or `[node.store] path`.

| File | Meaning |
|---|---|
| `<id>.sql.gz` | the backup, only ever created by renaming a verified `.part` |
| `<id>.json` | its manifest, written after the rename (temp file, fsync, rename, dir fsync) |
| `<id>.part` | a transfer in progress; deleted on mismatch, abort, agent start, or after 1 h |

A file without its manifest, a manifest without its file, a size mismatch or an unreadable
manifest is reported `corrupt`. Nodes never delete a `.sql.gz` or `.json` on their own; only
an authenticated `DELETE` removes them.

## Controller staging (since M2)

`<data-dir>/staging/` (0700) holds `<id>.sql.gz` and `<id>.json` with the same format,
written as mysqldump → gzip → (file, sha256) into `<id>.part`, then renamed. A dump that
fails, is cancelled, or prints `error` on stderr deletes its `.part`; an agent start deletes
any `.part` left behind.

Since M3, reconciliation (every 5 min) removes from staging every backup that is not among
the newest `[controller.staging] keep` (default 12; 0 means 12) of its database, is not
dumping, and is not still missing from a node that should hold it. It also removes
`<id>.sql.gz`/`<id>.json` files whose id the store does not know. The backup's
`staging_path` becomes `''`.

## Secrets (since M2)

The DB password is in the OS keyring: service `sevjala`, account `db`; `config.toml` holds
only `password_ref = "keyring:db"`. Linux without a Secret Service falls back to
`<data-dir>/secrets/db.enc` = 12-byte nonce ‖ AES-256-GCM ciphertext (additional data: the
name `db`), keyed by the 32 random bytes in `<data-dir>/secrets/key`; both files 0600 in a
0700 dir.

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
| `POST /code` (M1) | node | — | `{"code":"123456","expires_at":"RFC3339","agent_id":"…","fp":"sha256:…"}` (`fp` since M2) |
| `POST /discover` (M1) | controller | — | `{"agents":[Discovered…]}` |
| `POST /agents` (M1) | controller | `{"addr":"host[:port]"}` (port defaults to 17891) | `Discovered` |
| `GET /agents` (M1) | controller | — | `{"agents":[Agent…]}` |
| `POST /pair` (M1) | controller | `{"agent":"<agent>","code":"123456","fp":"<at least 16 hex>"}` (`fp` since M2: `sha256:`, spaces, `-` and `:` ignored, any case; must prefix the discovered fingerprint, else 409 before anything is sent) | `Agent` |
| `POST /agents/{agent}/ping` (M1) | controller | — | `{"agent":Agent,"rtt_ms":4,"uptime_s":7380,"health":"healthy"}` |
| `POST /db/detect` (M2) | controller | — | `Detection` |
| `PUT /db` (M2) | controller | `DBSettings`; tested against the server before anything is saved | `Detection` |
| `POST /backup/now` (M2) | controller | `{"db":"<name or empty for all configured>","tags":["…"]}` | `{"backups":[{"backup_id","database"}]}`; 409 while that database is dumping |
| `GET /backups` (M2) | controller | `?all=1` (M3) includes expired backups | `{"backups":[Backup…]}`, newest first |
| `GET /backups/{id}` (M2) | controller | — | `Backup`; 404 unknown |
| `GET /policies` (M3) | controller | — | `{"policies":[Policy…]}` in creation order |
| `POST /policies` (M3) | controller | `PolicyChange` with `name` and `keep` | `Policy`; 400 invalid, 409 exists |
| `PATCH /policies/{name}` (M3) | controller | `PolicyChange`; omitted fields keep their value | `Policy`; 404 unknown |
| `DELETE /policies/{name}` (M3) | controller | — | `{}`; 404 unknown, 409 for `manual`. Backups carrying the tag are kept. |
| `POST /retention/run` (M3) | controller | `{"dry_run":bool}` | `RetentionPlan` |
| anything else | | | 404 `not found` |

`Discovered`: `{"agent_id","hostname","addr":"ip:port","role","version","os","fp","paired":bool}`.
`Agent`: `{"agent_id","hostname","addr","role","version","fp","paired_at","last_seen","state","free_bytes","problem"}`
(`last_seen` omitted when never seen; `state` `online|warning|offline|error` and `free_bytes`
from the last poll, `problem` why it warned or failed, both since M3). `<agent>` is an exact agent_id, else a hostname (any
case), else a unique agent_id prefix of at least 4 characters; ambiguity is 409 listing the
candidates, no match is 404.
`Detection`: `{"dumper","client","server_version","flavour":"MariaDB|MySQL","databases":[{"name","tables","consistency":"--single-transaction|--lock-tables","non_innodb":["posts (MyISAM)"],"configured":bool}]}`.
`DBSettings`: `{"host","port","user","password","dumper","databases":[…]}`; an omitted field keeps
the current value, `databases` omitted or `null` keeps the list, `[]` clears it, `"password":""`
removes the stored password. Policy names match `^[a-z0-9][a-z0-9_-]{0,31}$`; since M3 every
tag of `backup now` must be a policy name (default `manual`).
`Policy`: `{"name","schedule","keep","to":[{"agent_id","hostname"}],"enabled","next_run"}`;
`schedule` is 5-field cron (`*`, `n`, `a-b`, `a,b`, `*/n`, `a-b/n`; day of week 0-7, Sunday 0
or 7) or `@hourly|@daily|@weekly|@monthly`, in the controller's local time; `""` never fires.
`to: []` is every paired node; `next_run` is omitted when disabled or never firing.
`PolicyChange`: `{"name","schedule","keep","to":["<agent>"…],"enabled"}`; `keep` 1-1000; `to`
omitted or `null` keeps the destinations, `[]` or `["all"]` is every node.
`RetentionPlan`: `{"dry_run","deletes":[Action],"sends":[Action],"kept":[Action],"unstaged":[id],"expired":[id]}`
with `Action` `{"backup_id","agent_id","hostname","reason"}`; a `kept` entry without
`backup_id` is a node skipped because it has no current inventory.
`Backup`: `{"backup_id","database","created_at","size","sha256","tags","state","error","bytes","copies":[{"agent_id","hostname","state","attempts","last_error","verified_at"}]}`;
`state` is `dumping` (with `bytes` so far) or `failed` (with `error`) before the backup is in
the store, then `created`, `distributed`, `partial` or (M3) `expired`. Copy states: `pending`,
`transferring`, `verified`, `failed`, `offline`, and since M3 `corrupt` and `deleted`.

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
| `store` | object | node only, since M2: `{"path","free_bytes","backups":N,"problems":[…]}` |

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

Version 2 (M2) adds:

```sql
CREATE TABLE backups (
  backup_id    TEXT PRIMARY KEY,
  "database"   TEXT NOT NULL,
  created_at   TEXT NOT NULL,          -- UTC RFC 3339
  size         INTEGER NOT NULL,
  sha256       TEXT NOT NULL,
  tags         TEXT NOT NULL,          -- JSON array
  staging_path TEXT NOT NULL,          -- relative to <data-dir>/staging
  state        TEXT NOT NULL           -- created | distributed | partial
);
CREATE TABLE backup_copies (
  backup_id   TEXT NOT NULL REFERENCES backups(backup_id),
  agent_id    TEXT NOT NULL,
  state       TEXT NOT NULL,           -- pending | transferring | verified | failed | offline
  attempts    INTEGER NOT NULL DEFAULT 0,
  last_error  TEXT NOT NULL DEFAULT '', -- kept across retries, cleared on verified
  verified_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (backup_id, agent_id)
);
```

A backup is `created` while any copy is `pending` or `transferring`, `distributed` when every
copy is `verified`, else `partial`. A backup with no paired nodes stays `created` in staging.

Version 3 (M3) adds the policies, seeded with the defaults:

```sql
CREATE TABLE policies (
  name         TEXT PRIMARY KEY,
  cron         TEXT NOT NULL,             -- '' = never scheduled
  keep         INTEGER NOT NULL,
  destinations TEXT NOT NULL DEFAULT '[]', -- JSON array of agent_id; [] = every paired node
  enabled      INTEGER NOT NULL DEFAULT 1  -- 0 stops scheduling; retention still applies keep
);
INSERT INTO policies (name, cron, keep) VALUES
  ('frequent', '*/10 * * * *', 12), ('hourly', '0 * * * *', 24),
  ('daily', '0 1 * * *', 30), ('monthly', '0 1 1 * *', 12), ('manual', '', 10);
```

Copy states add `corrupt` (the node reports it corrupt or with another sha256) and `deleted`
(removed by retention). The backup state ignores `deleted` copies: `created` while any copy is
`pending` or `transferring` or none is left, `distributed` when every remaining copy is
`verified`, else `partial`; `expired` (no copy and no staging file left) is set by
reconciliation and is undone only when a node shows a verified copy again. Agents' `state` is `online`, `warning`, `offline` or
`error`, with `free_bytes` and `last_seen` from the last answered poll.
