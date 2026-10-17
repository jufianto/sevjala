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
| `GET /backups/{id}` (M4) | paired controller cert | see below | see below |
| `POST /manage` (managers) | paired controller cert | 101, then a manager link (below) | 403 `this PC is not a manager` (the latest poll said so, or a controller); 400 without `Upgrade: sevjala-manage` |
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

Since managers (AD-21), every poll carries `X-Sevjala-Manager: 1|0` (whether an administrator
marked this node as a manager) and `X-Sevjala-Controller-Host: <controller hostname>`. The node
keeps the latest values in memory; a poll without `X-Sevjala-Manager` (an older controller)
leaves them as they were, and until the first poll with it the node does not know.

### Manager links (`POST /manage`, since managers)

The controller keeps 4 links open to every manager whose poll state is `online` or `warning`.
It dials like any LAN call (mutual TLS, pinned fingerprints) and sends `POST /manage` with
`Connection: Upgrade`, `Upgrade: sevjala-manage`. The node answers `HTTP/1.1 101 Switching
Protocols`, clears the connection's deadlines and pools it. From then on the roles swap on that
TLS connection: the node writes HTTP/1.1 requests and the controller answers them with its local
API, as a non-administrator caller whose account is `<X-Sevjala-By> via <manager hostname>`
(`X-Sevjala-By` is the account that asked on the manager). The node uses a link for one request
at a time and drops a link the controller closed; the controller closes a link idle for 5 min and
redials lost links with a 1 s → 15 s backoff, logging `manager_link_failed` once per failure
streak (a 404 says the node is an older build: "update Sevjala on <PC>"). Turning a manager off,
or removing the PC, closes its links at once; removing it also sends the node one last poll
with `X-Sevjala-Manager: 0` (3 s, best effort), since no later poll will reach it. A new
pairing on the node forgets the previous controller's hello. The node never dials.

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

### `GET /backups/{id}` (since M4)

Paired controller only; a restore fetches a node's copy with it. The body is the `.sql.gz` file
with the same `X-Sevjala-Sha256`, `X-Sevjala-Size` and `X-Sevjala-Manifest` headers as `PUT`,
plus `Content-Length`. The node holds the backup while it streams, so a `DELETE` of it waits
(503) until the stream ends; each write must leave within 60 s. The controller checks the
headers against the manifest, and size and sha256 against its catalog while it writes
`staging/<id>.restore.part`, which it deletes when the restore ends.

| Status | Meaning |
|---|---|
| 200 | the file follows |
| 400 | bad id |
| 403 | not the paired controller |
| 404 | not held here |
| 503 | being received or deleted right now |
| 422 | the file on disk is not the size its manifest says (damaged) |
| 409 | the agent is a controller |

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
  "source_host": "SERVER-01",
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
| `source_agent` | agent_id of the controller that made it |
| `source_host` | that controller's PC name, optional (since 0.3.0-alpha.4); a node names the controller after its newest backup that carries one |
| `dump_flags` | content flags, then `--single-transaction` (all tables InnoDB) or `--lock-tables` |
| `size`, `sha256` | of the `.sql.gz` file (gzip of the mysqldump output) |
| `tags` | `manual` unless `backup now --tag`; `pre-restore` for the backup `--replace-production` makes first (M4) |

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
Since M4 a restore that fetches from a node, or restores an exported file, writes
`<id>.restore.part` here while hashing and loads that copy; it is deleted when the restore
ends, and an agent start deletes it like any `.part`. A `--replace-production` that fails after
the drop leaves `<data-dir>/replace-incomplete-<db>` (one line: the pre-restore backup id);
while it exists backups of `<db>` are refused, and a replace that succeeds removes it.

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

Transport: HTTP/1.1 over `\\.\pipe\sevjala` (Windows, SDDL `O:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12008b;;;IU)` since M5: SYSTEM and Administrators full, interactive users read/write without create-instance) or
`/var/run/sevjala.sock` (macOS/Linux, `0660 root:sevjala`). A non-default data dir uses
`<data-dir>/sevjala.sock` or `\\.\pipe\sevjala-<first 8 hex of sha256(data dir)>` (default DACL).
On the default pipe the client opens with `GENERIC_READ|FILE_WRITE_DATA` and refuses a pipe
whose owner is not Administrators or SYSTEM (another user's pipe squatting the name).

Callers (M5, AD-20): every request carries who called, from the OS: on Windows the pipe
client's process token (elevated, or SYSTEM, is an administrator), on Linux `SO_PEERCRED`, on
macOS `LOCAL_PEERCRED` (uid 0, or the account running the agent — root for the service, the developer for one started by hand — is an administrator). Since M7 (AD-20 amended) only two requests are admin-only; from anyone
else they answer 403 `{"error":"<action> needs administrator rights","code":"admin_required"}`,
and the CLI re-runs the command elevated (UAC, `sudo`):

Over a manager link (AD-21) the caller is never an administrator: an admin-only request answers
403 `{"error":"<action> needs administrator rights on <controller> itself","code":"controller_admin_required"}`,
and the CLI shows it without offering to elevate.

| Request | Admin-only when |
|---|---|
| `PUT /db` | `dumper` is set (the service would run that program) |
| `POST /restore` | `file` is set (the service would run that file's SQL with the stored password) |

Everything else is a normal-user action (until M7 also pairing, codes, removal, export, a
database host or port, tagged backups and policy changes that delete). The caller's account is
recorded per connection (Windows `DOMAIN\user` from the client token, else its SID; Unix the
peer uid's user name, else the uid) and every change event logs it as `"by"`; events the agent
starts itself, such as a scheduled backup, have none. `PUT /db` that changes `host`, `port` or
`user` without `password` is 400 `changing the database host, port or user needs its password
again: add --password (or --no-password)`.

Destructive requests (M7) answer 409
`{"error":"…","code":"confirm_required","word":"<word>","deletes":N,"hosts":["<hostname>"…]}`
until they repeat the word in `confirm`: `POST /policies` and `PATCH /policies/{name}` when the
change would delete backups (word: the policy name; `deletes` counts copies; PCs not polled
count with the copies the store has verified there; two PCs with one hostname show as
`<hostname> (<agent id prefix>)`; a clock behind a backup is previewed as if it had caught up),
`DELETE /agents/{agent}?force=true` while backups have their only copy there (word: its hostname), and `POST /restore` with `replace`
(word: the database, `deletes` 0). The CLI asks for the word and takes `--confirm WORD`.

Errors are `{"error": "<message shown to the operator>"}`, with `"code"` when a client acts on it (`admin_required`, M5; `confirm_required`, M7); a route for the other role is 409
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
| `PUT /agents/{agent}/manager` (managers) | controller | `{"manager":true|false}` | `Agent` (with `manager`). Administrator only (`admin_required`); 409 for the controller itself (`<host> is the controller; only a backup PC can be a manager`); event `manager_set` |
| `DELETE /agents/{agent}[?force=true[&confirm=<hostname>]]` | controller | — | `Agent` removed. 409 while a policy sends only to it; while it holds the only verified copy of a configured database (no other PC and not the controller's own copy holds one; `force` does not help, M7); without `force` while a live backup's only verified copy is on it; with `force`, `confirm_required` until `confirm` is its hostname (M7). Drops its copy records and its policy destinations; nothing is sent to the node. |
| `GET /db` (M7) | controller | — | `DBConfig` |
| `POST /db/detect` (M2) | controller | — | `Detection` |
| `PUT /db` (M2) | controller | `DBSettings`; tested against the server before anything is saved | `Detection` |
| `POST /backup/now` (M2) | controller | `{"db":"<name or empty for all configured>","tags":["…"]}` | `{"backups":[{"backup_id","database"}]}`; 409 while that database is dumping |
| `GET /backups` (M2) | controller | `?all=1` (M3) includes expired backups | `{"backups":[Backup…]}`, newest first |
| `GET /backups/{id}` (M2) | controller | — | `Backup`; 404 unknown |
| `GET /policies` (M3) | controller | — | `{"policies":[Policy…]}` in creation order |
| `POST /policies` (M3) | controller | `PolicyChange` with `name` and `keep` | `Policy`; 400 invalid, 409 exists or `confirm_required` (M7); with `dry_run` nothing is saved and `deletes` lists what it would delete |
| `PATCH /policies/{name}` (M3) | controller | `PolicyChange`; omitted fields keep their value | `Policy`; 404 unknown, 409 `confirm_required` (M7); `dry_run` as for `POST` |
| `DELETE /policies/{name}` (M3) | controller | — | `{}`; 404 unknown, 409 for `manual` and `pre-restore` (M4). Backups carrying the tag are kept. |
| `POST /retention/run` (M3) | controller | `{"dry_run":bool}` | `RetentionPlan` |
| `POST /restore` (M4) | controller | `RestoreRequest` | `Restore` as started (`state` `running`), or with `dry_run` what would run (`state` `""`). 400 bad input or both `backup` and `file`; 404 no such backup, or none of that database to restore; 409 a restore is running, the target exists, `replace` without `confirm` equal to the database (`confirm_required` since M7), `replace` while that database is dumping or does not exist, or the backup expired. On a node: 409 saying to export the file and restore it with `--file` on the controller. |
| `GET /restore` (M4) | controller | — | the running or last `Restore`; 404 when none ran since the agent started |
| `GET /logs[?limit=N]` (M7) | both | — | `{"entries":[…]}`: the JSON lines of `sevjala.log` and its rotations, newest first; `limit` 1-2000 (default 500, else 400); lines that are not JSON are skipped |
| `GET /held` | both | — | `{"backups":[Manifest…]}`, oldest first: the complete backups this PC holds (a node's store, the controller's staging). Read-only: unlike the scan behind the LAN `GET /status`, it removes nothing and reports no problems. |
| `GET /held/{id}` | both | — | The `.sql.gz` bytes, `Content-Length` = manifest size, `Content-Disposition: attachment; filename=<database>_<yyyy-mm-dd_hh-mm-ss>.sql.gz` in the agent's local time (M7). 400 not a backup id, 404 not here, 409 being received or deleted (a node holds the backup until the stream ends, so a `DELETE` from the controller waits) or damaged. The CLI checks size and sha256 itself. |
| anything else | | | 404 `not found` |

`Discovered`: `{"agent_id","hostname","addr":"ip:port","role","version","os","fp","paired":bool}`.
`Agent`: `{"agent_id","hostname","addr","role","version","fp","paired_at","last_seen","state","free_bytes","problem"}`
(`last_seen` omitted when never seen; `state` `online|warning|offline|error` and `free_bytes`
from the last poll, `problem` why it warned or failed, both since M3). `<agent>` is an exact agent_id, else a hostname (any
case), else a unique agent_id prefix of at least 4 characters; ambiguity is 409 listing the
candidates, no match is 404.
`Detection`: `{"dumper","client","server_version","flavour":"MariaDB|MySQL","databases":[{"name","tables","consistency":"--single-transaction|--lock-tables","non_innodb":["posts (MyISAM)"],"configured":bool}]}`.
`DBConfig` (M7): `{"host","port","user","dumper","databases":[…],"has_password":bool}`; the password itself is never sent.
`DBSettings`: `{"host","port","user","password","dumper","databases":[…]}`; an omitted field keeps
the current value, `databases` omitted or `null` keeps the list, `[]` clears it, `"password":""`
removes the stored password. Policy names match `^[a-z0-9][a-z0-9_-]{0,31}$`; since M3 every
tag of `backup now` must be a policy name (default `manual`).
`Policy`: `{"name","schedule","keep","to":[{"agent_id","hostname"}],"enabled","next_run","deletes"}`
(`deletes`, M7: `[Action]` the change would delete, on a dry run only);
`schedule` is 5-field cron (`*`, `n`, `a-b`, `a,b`, `*/n`, `a-b/n`; day of week 0-7, Sunday 0
or 7) or `@hourly|@daily|@weekly|@monthly`, in the controller's local time; `""` never fires.
`to: []` is every paired node; `next_run` is omitted when disabled or never firing.
`PolicyChange`: `{"name","schedule","keep","to":["<agent>"…],"enabled","confirm","dry_run"}` (the last two since M7); `keep` 1-1000; `to`
omitted or `null` keeps the destinations, `[]` or `["all"]` is every node.
`RetentionPlan`: `{"dry_run","deletes":[Action],"sends":[Action],"kept":[Action],"unstaged":[id],"expired":[id]}`
with `Action` `{"backup_id","agent_id","hostname","reason"}`; a `kept` entry without
`backup_id` is a node skipped because it has no current inventory.
`Backup`: `{"backup_id","database","created_at","size","sha256","tags","state","error","bytes","copies":[{"agent_id","hostname","state","attempts","last_error","verified_at"}]}`;
`state` is `dumping` (with `bytes` so far) or `failed` (with `error`) before the backup is in
the store, then `created`, `distributed`, `partial` or (M3) `expired`. Copy states: `pending`,
`transferring`, `verified`, `failed`, `offline`, and since M3 `corrupt` and `deleted`.
`RestoreRequest` (M4): `{"backup":"<id>|latest","db","into","replace":bool,"confirm","file","dry_run":bool}`.
`latest` (or empty) is the newest backup of `db`, or of the only configured database, that is
in the controller's own copy or has a verified copy on an answering (online or warning) node. `into` defaults to
`<database>_restored_<yyyymmdd_hhmm>` (the backup's local creation time; names are 1-64 of
`[A-Za-z0-9_$-]`, not starting with `-`) and must not exist. `replace` targets the backup's own database instead and
needs `confirm` equal to it. `file` is an absolute path to an exported `.sql.gz` with its
`.json` beside it.
`Restore` (M4): `{"backup_id","backup_created_at","database","target","replace","source","state","step","bytes","size","pre_restore_id","tables","error","started_at","finished_at"}`;
`state` `running`, `done` or `failed`; `step` says in words what runs now, `bytes` of `size`
its progress; `source` is `the controller's own copy`, a node's hostname or the file path;
`pre_restore_id` is the backup made just before a replace; `tables` counts the base tables
loaded.

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
| `store` | object | node only, since M2: `{"path","free_bytes","backups":N,"problems":[…]}`; `sources` since 0.3.0-alpha.4: `[{"agent_id","hostname","backups":N,"current":bool}]`, the controllers its backups came from, the paired one (`current`) first. A pairing replaces the controller and keeps the old one's backups, which nothing deletes |
| `hostname` | string | since M5 |
| `health` | object | since M5, `Health` below; omitted while the agent starts |
| `controller` | object | node only, paired, since 0.3.0-alpha.4: `{"agent_id","hostname","paired_at"}`; `hostname` is empty until a backup from it carries one. Since managers: `manager` (the latest poll said this PC is a manager), `links` (manager links waiting idle), `last_contact` (the last request from the controller); `hostname` also comes from the poll's `X-Sevjala-Controller-Host` |

`Health` (M5): `{"level":"ok|warning|problem","headline","items":[{"level","text"}],"databases":[DBHealth],"next_backup_at","next_policy","agents":[AgentHealth],"free_bytes","controller_contact_at"}`.
`items` lists problems before warnings; with any item, `headline` and `level` are the first
one's, else `headline` says what is protected. Controller only: `databases`
(`{"name","last_good","last_good_at","size","copies","planned","last_failed_at","last_error"}`:
the newest backup with a verified copy, its verified and planned copies, the last failed dump),
`next_backup_at`/`next_policy`, `agents` (`{"agent_id","hostname","state","last_seen","waiting"}`, `agent_id` since M6, `waiting`
copies not yet on it). Node only: `controller_contact_at`, the last request from its paired
controller (since the agent started). `free_bytes` is staging on a controller, the store on a node.
The verdict reads only the store and memory, never MySQL; the tray asks every 15 s.

| Level | Controller | Node |
|---|---|---|
| problem | no backup PC paired; a configured database without a verified copy (with a PC paired), or two runs of an enabled schedule passed since its newest one (and at least 1 h); the last dump of a database failed; a replace left a database partly loaded; staging free under max(1 GiB, 2 × the newest backup of the largest database) | store free under `min_free_bytes` |
| warning | a node offline or in error (with the copies waiting for it) or warning; the newest backup still partial after 30 min on an answering node; a damaged copy the controller cannot replace (not in staging); no enabled schedule; no database configured | not paired; paired but no controller request for 15 min; each store problem |

## Dashboard (`sevjala ui`, since M6)

`sevjala ui` listens on `127.0.0.1:<random port>` as the user who ran it and prints
`http://127.0.0.1:<port>/?token=<64 hex>`. It opens that link on Windows directly; on macOS/Linux,
where any account can read a command line, it opens a file `open.html` in a fresh temp folder
(0700, file 0600, removed on exit) that forwards to the link. The token works once: it sets the
cookie `sevjala_session_<port>` (random, `HttpOnly`, `SameSite=Strict`, path `/`, `Max-Age` 7 days;
named per port because browsers keep cookies per host) and answers 200 with a page that forwards
to `/` (a redirect would lose a Strict cookie on a chain that began at the file); a used or wrong
token is 403. The process
stops 60 s after its last `/events` client leaves (counted from start before the first).

Every response carries `Content-Security-Policy: default-src 'self'; img-src 'self' data:;
style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'`,
`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
`Cache-Control: no-store`. A `Host` other than `127.0.0.1:<port>` is 403 (DNS rebinding);
without the cookie everything else is 401.

| Path | Meaning |
|---|---|
| `/`, `/assets/…` | the embedded Svelte build (`ui/dist`); a binary built without it answers `/` with 404 "built without the dashboard" |
| `/api/<route>` | the local API route `<route>` (table above), as that user; `Cookie` and `X-Sevjala-UI` are removed. Anything but `GET`/`HEAD` needs `X-Sevjala-UI: 1` (403 otherwise). An agent that does not answer is 502 `{"error":"The Sevjala service is not running"}` |
| `/events` | `text/event-stream`. Every 2 s the proxy asks `GET /status` (and `GET /agents` on a controller) and sends only changes (nothing while `/agents` fails, so the page keeps its list): `event: state` with `{"status":Status,"agents":[Agent…]}` (plus `"via":"<this PC>"` with `--controller`), or `event: down` with `{"error":"…"}`; a `: ping` comment after 15 s of silence |

### The relay (`/controller/…`, since managers)

On a node, the local API also answers `/controller/<route>`: it sends `<route>` to the
controller over a free manager link (waiting up to 20 s for one) and passes the answer back
unchanged. `sevjala --controller <command>` and `sevjala ui --controller` use it, with at least
30 s per call. `Agent` (`GET /agents`) has `"manager":bool`. Relay errors:

| Status | When | Message |
|---|---|---|
| 409 | on a controller | `this PC is the controller; run the command without --controller` |
| 409 | not paired | `this PC is not paired with a controller` |
| 403 | the latest poll says not a manager | `this PC is not a manager of <host> (on <host>, an administrator runs: sevjala agents manager <this hostname> on)` |
| 503 | no poll yet since the agent started | `the controller has not contacted this PC since Sevjala started here (is it on and on the network?)` |
| 503 | no link within 20 s | `<host> has not connected to this PC for <time> (is it on and on the network?)` |
| 502 | a link broke during the request | `the link to <host> broke mid-way; try again` |

With `--controller`, `sevjala ui` serves the controller's dashboard: `/api/<route>` goes to
`/controller/<route>` and `/events` asks through the relay with a 30 s limit.

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

Copy states add `corrupt` (the node reports it corrupt or with another sha256, or the controller read it and its sha256 did not match: `last_error` "damaged: its sha256 did not match when the controller read it", and retention replaces it although the node still lists it) and `deleted`
(removed by retention). The backup state ignores `deleted` copies: `created` while any copy is
`pending` or `transferring` or none is left, `distributed` when every remaining copy is
`verified`, else `partial`; `expired` (no copy and no staging file left) is set by
reconciliation and is undone only when a node shows a verified copy again. Agents' `state` is `online`, `warning`, `offline` or
`error`, with `free_bytes` and `last_seen` from the last answered poll.
