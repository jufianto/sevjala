# Protocol and on-disk formats

The living contract for every wire message and file format. Update it in the same PR as
any change to one (definition of done).

Until M1 implements them, the blueprint is the contract:

| Format | Blueprint section |
|---|---|
| Discovery datagrams (`SEVJALA/1 DISCOVER`, JSON reply) | [Coordination protocol → Discovery](./BLUEPRINT.md#discovery-controller--all) |
| Pairing (`POST /pair`, HMAC proof) | [Coordination protocol → Pairing](./BLUEPRINT.md#pairing-code-based-trust-bootstrap) |
| LAN API routes and headers | [Coordination protocol → LAN API](./BLUEPRINT.md#lan-api-served-by-every-agent-used-only-by-the-controller) |
| Local API routes | [Coordination protocol → Local API](./BLUEPRINT.md#local-api-pipe--socket-http-no-tls) |
| `config.toml` | [Data model → config.toml](./BLUEPRINT.md#configtoml-minimal-example) |
| Manifest `<backup_id>.json` | [Data model → Manifest](./BLUEPRINT.md#manifest-backup_idjson-identical-on-controller-and-nodes) |
| `GET /status` response | [Data model → GET /status](./BLUEPRINT.md#get-status-response) |
| Node store directory | [Data model → Node store directory](./BLUEPRINT.md#node-store-directory) |

When a milestone implements a format, move its exact definition here (field types,
required/optional, error responses) and leave a link in the blueprint.

## Local API (implemented)

Transport: HTTP/1.1 over `\\.\pipe\sevjala` (Windows, SDDL `D:P(A;;GA;;;BA)(A;;GA;;;SY)`) or
`/var/run/sevjala.sock` (macOS/Linux, `0660 root:sevjala`). A non-default data dir uses
`<data-dir>/sevjala.sock` or `\\.\pipe\sevjala-<first 8 hex of sha256(data dir)>` (default DACL).

### `GET /status` (since P0)

`200 application/json`

```json
{"version": "0.1.0", "role": "node", "pid": 4120, "uptime_s": 133, "data_dir": "C:\\ProgramData\\Sevjala"}
```

| Field | Type | Meaning |
|---|---|---|
| `version` | string | agent build version (`dev` for local builds) |
| `role` | string | `controller` or `node` |
| `pid` | int | agent process id |
| `uptime_s` | int | seconds since the agent started |
| `data_dir` | string | absolute data dir |

Any other path: `404 {"error":"not found"}`.
