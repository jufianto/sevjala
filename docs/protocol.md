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
